package db

import (
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

func TestEvaluateCostPrefersReportedCost(t *testing.T) {
	pricer := stubPricer{"known": {Input: 1}}
	reported := 7.0
	usage := canon.Usage{InputTokens: 2, ReportedCostUSD: &reported}

	got, err := EvaluateCost(pricer, "", "known", usage)
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount.String() != "7" || got.Reported.String() != "7" || got.Estimated != 0 {
		t.Errorf("cost = %+v", got)
	}
}

func TestEvaluateCostCalculatesZeroAndMissingReports(t *testing.T) {
	pricer := stubPricer{"known": {Input: 1}}
	zero := 0.0
	usage := canon.Usage{InputTokens: 2, ReportedCostUSD: &zero}

	got, err := EvaluateCost(pricer, "", "known", usage)
	if err != nil || got.Amount.String() != "2" || got.Estimated.String() != "2" {
		t.Fatalf("zero report = %+v, %v", got, err)
	}

	usage.ReportedCostUSD = nil
	got, err = EvaluateCost(pricer, "", "known", usage)
	if err != nil || got.Amount.String() != "2" || got.Estimated.String() != "2" {
		t.Fatalf("missing report = %+v, %v", got, err)
	}
}

func TestEvaluateCostUnknownModelIsUnpriced(t *testing.T) {
	usage := canon.Usage{InputTokens: 3}
	got, err := EvaluateCost(stubPricer{}, "", "missing", usage)
	if err != nil || got.Unpriced.InputTokens != 3 {
		t.Errorf("cost = %+v, %v", got, err)
	}
}

func TestEvaluateCostUsesFixedPoint(t *testing.T) {
	pricer := stubPricer{"tiny": pricing.Rate{Input: 4e-10, Output: 4e-10}}
	got, err := EvaluateCost(pricer, "", "tiny", canon.Usage{InputTokens: 1, OutputTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount != 1 {
		t.Fatalf("amount = %d, want one nanodollar", got.Amount)
	}
}
