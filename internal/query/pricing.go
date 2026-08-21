package query

import (
	"context"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// PricingInfo explains the immutable price source and, optionally, one model
// resolution. It is diagnostic provenance, not another cost mode.
type PricingInfo struct {
	Source         string       `json:"source,omitempty"`
	FetchedAt      string       `json:"fetchedAt,omitempty"`
	Algorithm      string       `json:"algorithm,omitempty"`
	Fingerprint    string       `json:"fingerprint,omitempty"`
	RollupsCurrent bool         `json:"rollupsCurrent"`
	AutoMode       string       `json:"autoMode"`
	RequestedModel string       `json:"requestedModel,omitempty"`
	ResolvedModel  string       `json:"resolvedModel,omitempty"`
	Resolved       bool         `json:"resolved"`
	Rates          *PricingRate `json:"rates,omitempty"`
}

// PricingRate uses pointers for optional cache dimensions, preserving the
// difference between an absent rate and an intentional numeric zero.
type PricingRate struct {
	Input        float64  `json:"input"`
	Output       float64  `json:"output"`
	CacheWrite   *float64 `json:"cacheWrite,omitempty"`
	CacheWrite1h *float64 `json:"cacheWrite1h,omitempty"`
	CacheRead    *float64 `json:"cacheRead,omitempty"`
}

type diagnosticPricer interface {
	db.Pricer
	db.FingerprintedPricer
	Provenance() (source, fetchedAt, algorithm string)
	Resolve(model string) (pricing.Rate, string, bool)
}

// Pricing returns pricing-table provenance and optional model-resolution
// details. The decision rule is stated explicitly so callers can interpret a
// reported zero without reverse-engineering SQL.
func (s *Service) Pricing(ctx context.Context, model string) (PricingInfo, error) {
	info := PricingInfo{
		AutoMode:       "reported non-zero cost; otherwise calculate non-zero tokens; missing rates remain unpriced",
		RequestedModel: model,
	}
	if p, ok := s.pricer.(diagnosticPricer); ok {
		info.Source, info.FetchedAt, info.Algorithm = p.Provenance()
		info.Fingerprint = p.Fingerprint()
		dirty, err := s.store.RollupsNeedRegeneration(ctx, p)
		if err != nil {
			return PricingInfo{}, err
		}
		info.RollupsCurrent = !dirty
		if model != "" {
			rate, key, found := p.Resolve(model)
			info.Resolved, info.ResolvedModel = found, key
			if found {
				out := &PricingRate{Input: rate.Input, Output: rate.Output}
				if v, ok := rate.CacheWriteRate(); ok {
					out.CacheWrite = floatPtr(v)
				}
				if v, ok := rate.CacheWrite1hRate(); ok {
					out.CacheWrite1h = floatPtr(v)
				}
				if v, ok := rate.CacheReadRate(); ok {
					out.CacheRead = floatPtr(v)
				}
				info.Rates = out
			}
		}
		return info, nil
	}
	// Synthetic pricers used by embedders/tests have no provenance but can
	// still answer a direct lookup.
	if model != "" {
		rate, found := s.pricer.Lookup(model)
		info.Resolved = found
		if found {
			info.ResolvedModel = model
			info.Rates = &PricingRate{Input: rate.Input, Output: rate.Output}
		}
	}
	info.RollupsCurrent = true
	return info, nil
}

func floatPtr(v float64) *float64 { return &v }
