package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// CostMode selects which provenance is allowed to contribute to a cost.
type CostMode string

const (
	CostModeAuto      CostMode = "auto"
	CostModeCalculate CostMode = "calculate"
	CostModeDisplay   CostMode = "display"

	// ReportedCostNanosExpr is the canonical exact reported-cost read. All
	// usage queries alias message_usage as u. The REAL fallback supports rows
	// inserted by old embedders and tests; schema-v15 ingestion writes nanos.
	ReportedCostNanosExpr = `CASE WHEN u.reported_cost_usd IS NULL THEN NULL
		ELSE COALESCE(u.reported_cost_nanos,
		CAST(ROUND(u.reported_cost_usd * 1000000000) AS INTEGER)) END`
)

// ParseCostMode applies the public default and validates an explicit value.
func ParseCostMode(raw string) (CostMode, error) {
	mode := CostMode(strings.ToLower(strings.TrimSpace(raw)))
	if mode == "" {
		return CostModeAuto, nil
	}
	switch mode {
	case CostModeAuto, CostModeCalculate, CostModeDisplay:
		return mode, nil
	default:
		return "", fmt.Errorf("cost mode must be auto, calculate, or display")
	}
}

// CostResult keeps exact amount provenance and completeness separate. Unpriced
// is rate-resolution failure; Unreported is usage hidden by display mode
// because the agent supplied no cost.
type CostResult struct {
	Amount     pricing.Amount
	Reported   pricing.Amount
	Estimated  pricing.Amount
	Unpriced   canon.Usage
	Unreported canon.Usage
}

// EvaluateCost applies one cost mode to one canonical usage row. Pricing is
// intentionally row-scoped so future request tiers and effective dates cannot
// be selected from an aggregate that crossed a threshold or rate boundary.
func EvaluateCost(p Pricer, mode CostMode, provider, model string, u canon.Usage) (CostResult, error) {
	var reported *pricing.Amount
	if u.ReportedCostUSD != nil {
		amount, err := pricing.AmountFromUSD(*u.ReportedCostUSD)
		if err != nil {
			return CostResult{}, err
		}
		reported = &amount
	}
	return EvaluateCostAt(p, mode, provider, model, time.Time{}, u, reported)
}

// EvaluateCostAt is the exact storage-facing evaluator. Reported is already
// quantized at ingestion, and at selects historical cards when the pricer
// supports them.
func EvaluateCostAt(p Pricer, mode CostMode, provider, model string, at time.Time, u canon.Usage, reported *pricing.Amount) (CostResult, error) {
	var result CostResult
	total := usageTotal(u)
	hasReported := reported != nil
	reportedUsableInAuto := hasReported && (*reported != 0 || total == 0)

	if mode == CostModeDisplay {
		if !hasReported {
			result.Unreported = positiveUsage(u)
			return result, nil
		}
		result.Amount, result.Reported = *reported, *reported
		return result, nil
	}
	if mode == CostModeAuto && reportedUsableInAuto {
		result.Amount, result.Reported = *reported, *reported
		return result, nil
	}
	if total == 0 {
		return result, nil
	}

	pricingModel := PricingModel(provider, model)
	var rate pricing.Rate
	var ok bool
	if contextual, supportsContext := p.(interface {
		ResolveAt(string, time.Time, int64) (pricing.Rate, pricing.Resolution, bool)
	}); supportsContext {
		input := max(u.InputTokens, 0) + max(u.CacheReadTokens, 0) + max(u.CacheWriteTokens, 0)
		rate, _, ok = contextual.ResolveAt(pricingModel, at, input)
	} else {
		rate, ok = p.Lookup(pricingModel)
		if ok {
			input := max(u.InputTokens, 0) + max(u.CacheReadTokens, 0) + max(u.CacheWriteTokens, 0)
			rate, _ = rate.ForInput(input)
		}
	}
	if !ok {
		result.Unpriced = positiveUsage(u)
		return result, nil
	}
	amount, unpriced, err := rate.PriceAmount(u)
	if err != nil {
		return CostResult{}, err
	}
	result.Amount, result.Estimated, result.Unpriced = amount, amount, unpriced
	return result, nil
}

// ParseCostTime accepts both stored RFC3339 instants and price-card dates.
// A zero result means the row has no usable request time and current pricing
// semantics apply.
func ParseCostTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

func positiveUsage(u canon.Usage) canon.Usage {
	return canon.Usage{
		InputTokens:        max(u.InputTokens, 0),
		OutputTokens:       max(u.OutputTokens, 0),
		CacheReadTokens:    max(u.CacheReadTokens, 0),
		CacheWriteTokens:   max(u.CacheWriteTokens, 0),
		CacheWrite1hTokens: min(max(u.CacheWrite1hTokens, 0), max(u.CacheWriteTokens, 0)),
	}
}
