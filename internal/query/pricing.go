package query

import (
	"context"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// PricingInfo explains the immutable price source and, optionally, one model
// resolution. It is diagnostic provenance for the single cost policy.
type PricingInfo struct {
	Source           string       `json:"source,omitempty"`
	FetchedAt        string       `json:"fetchedAt,omitempty"`
	Algorithm        string       `json:"algorithm,omitempty"`
	Fingerprint      string       `json:"fingerprint,omitempty"`
	RollupsCurrent   bool         `json:"rollupsCurrent"`
	CostPolicy       string       `json:"costPolicy"`
	RequestedModel   string       `json:"requestedModel,omitempty"`
	ResolvedModel    string       `json:"resolvedModel,omitempty"`
	Resolved         bool         `json:"resolved"`
	RequestedAt      string       `json:"requestedAt,omitempty"`
	InputTokens      int64        `json:"inputTokens,omitempty"`
	EffectiveFrom    string       `json:"effectiveFrom,omitempty"`
	EffectiveTo      string       `json:"effectiveTo,omitempty"`
	RateSource       string       `json:"rateSource,omitempty"`
	AboveInputTokens int64        `json:"aboveInputTokens,omitempty"`
	Rates            *PricingRate `json:"rates,omitempty"`
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
	ResolveAt(model string, at time.Time, inputTokens int64) (pricing.Rate, pricing.Resolution, bool)
}

// Pricing returns pricing-table provenance and optional model-resolution
// details. The decision rule is stated explicitly so callers can interpret a
// reported zero without reverse-engineering SQL.
func (s *Service) Pricing(ctx context.Context, model string) (PricingInfo, error) {
	return s.PricingAt(ctx, model, "", 0)
}

// PricingAt resolves historical and request-tier context for diagnostics.
func (s *Service) PricingAt(ctx context.Context, model, atRaw string, inputTokens int64) (PricingInfo, error) {
	if inputTokens < 0 {
		return PricingInfo{}, badRequest("input_tokens must be non-negative")
	}
	var at time.Time
	if atRaw != "" {
		at = db.ParseCostTime(atRaw)
		if at.IsZero() {
			return PricingInfo{}, badRequest("at must be RFC3339 or YYYY-MM-DD")
		}
	}
	info := PricingInfo{
		CostPolicy:     "reported non-zero cost; otherwise calculate from reported tokens; missing rates remain unpriced",
		RequestedModel: model,
		RequestedAt:    atRaw,
		InputTokens:    inputTokens,
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
			rate, resolution, found := p.ResolveAt(model, at, inputTokens)
			info.Resolved, info.ResolvedModel = found, resolution.Key
			info.EffectiveFrom, info.EffectiveTo = resolution.EffectiveFrom, resolution.EffectiveTo
			info.RateSource, info.AboveInputTokens = resolution.Source, resolution.AboveInputTokens
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
