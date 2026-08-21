package db

import (
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// CostMode selects which provenance is allowed to contribute to a cost.
type CostMode string

const (
	CostModeAuto      CostMode = "auto"
	CostModeCalculate CostMode = "calculate"
	CostModeDisplay   CostMode = "display"
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
	var result CostResult
	total := usageTotal(u)
	hasReported := u.ReportedCostUSD != nil
	reportedUsableInAuto := hasReported && (*u.ReportedCostUSD != 0 || total == 0)

	if mode == CostModeDisplay {
		if !hasReported {
			result.Unreported = positiveUsage(u)
			return result, nil
		}
		amount, err := pricing.AmountFromUSD(*u.ReportedCostUSD)
		if err != nil {
			return CostResult{}, err
		}
		result.Amount, result.Reported = amount, amount
		return result, nil
	}
	if mode == CostModeAuto && reportedUsableInAuto {
		amount, err := pricing.AmountFromUSD(*u.ReportedCostUSD)
		if err != nil {
			return CostResult{}, err
		}
		result.Amount, result.Reported = amount, amount
		return result, nil
	}
	if total == 0 {
		return result, nil
	}

	rate, ok := p.Lookup(PricingModel(provider, model))
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

func positiveUsage(u canon.Usage) canon.Usage {
	return canon.Usage{
		InputTokens:        max(u.InputTokens, 0),
		OutputTokens:       max(u.OutputTokens, 0),
		CacheReadTokens:    max(u.CacheReadTokens, 0),
		CacheWriteTokens:   max(u.CacheWriteTokens, 0),
		CacheWrite1hTokens: min(max(u.CacheWrite1hTokens, 0), max(u.CacheWriteTokens, 0)),
	}
}
