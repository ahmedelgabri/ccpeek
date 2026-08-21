package db

import (
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

func TestParseCostMode(t *testing.T) {
	for input, want := range map[string]CostMode{"": CostModeAuto, "AUTO": CostModeAuto, "calculate": CostModeCalculate, "display": CostModeDisplay} {
		got, err := ParseCostMode(input)
		if err != nil || got != want {
			t.Errorf("ParseCostMode(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseCostMode("invoice"); err == nil {
		t.Error("invalid mode unexpectedly accepted")
	}
}

func TestEvaluateCostModes(t *testing.T) {
	pricer := stubPricer{"known": {Input: 1}}
	reported := 7.0
	usage := canon.Usage{InputTokens: 2, ReportedCostUSD: &reported}

	auto, err := EvaluateCost(pricer, CostModeAuto, "", "known", usage)
	if err != nil {
		t.Fatal(err)
	}
	if auto.Amount.String() != "7" || auto.Reported.String() != "7" || auto.Estimated != 0 {
		t.Errorf("auto = %+v", auto)
	}
	calculated, err := EvaluateCost(pricer, CostModeCalculate, "", "known", usage)
	if err != nil {
		t.Fatal(err)
	}
	if calculated.Amount.String() != "2" || calculated.Estimated.String() != "2" || calculated.Reported != 0 {
		t.Errorf("calculate = %+v", calculated)
	}
	display, err := EvaluateCost(pricer, CostModeDisplay, "", "known", usage)
	if err != nil {
		t.Fatal(err)
	}
	if display.Amount.String() != "7" || usageTotal(display.Unreported) != 0 {
		t.Errorf("display = %+v", display)
	}
}

func TestEvaluateCostZeroAndMissingReports(t *testing.T) {
	pricer := stubPricer{"known": {Input: 1}}
	zero := 0.0
	usage := canon.Usage{InputTokens: 2, ReportedCostUSD: &zero}

	auto, err := EvaluateCost(pricer, CostModeAuto, "", "known", usage)
	if err != nil || auto.Amount.String() != "2" || auto.Estimated.String() != "2" {
		t.Fatalf("zero auto = %+v, %v", auto, err)
	}
	display, err := EvaluateCost(pricer, CostModeDisplay, "", "known", usage)
	if err != nil || display.Amount != 0 || usageTotal(display.Unreported) != 0 {
		t.Fatalf("zero display = %+v, %v", display, err)
	}

	usage.ReportedCostUSD = nil
	display, err = EvaluateCost(pricer, CostModeDisplay, "", "known", usage)
	if err != nil || display.Amount != 0 || display.Unreported.InputTokens != 2 {
		t.Fatalf("missing display = %+v, %v", display, err)
	}
}

func TestEvaluateCostUnknownModelByMode(t *testing.T) {
	usage := canon.Usage{InputTokens: 3}
	for _, mode := range []CostMode{CostModeAuto, CostModeCalculate} {
		got, err := EvaluateCost(stubPricer{}, mode, "", "missing", usage)
		if err != nil || got.Unpriced.InputTokens != 3 {
			t.Errorf("%s = %+v, %v", mode, got, err)
		}
	}
	got, err := EvaluateCost(stubPricer{}, CostModeDisplay, "", "missing", usage)
	if err != nil || got.Unreported.InputTokens != 3 || usageTotal(got.Unpriced) != 0 {
		t.Errorf("display = %+v, %v", got, err)
	}
}

func TestEvaluateCostUsesFixedPoint(t *testing.T) {
	pricer := stubPricer{"tiny": pricing.Rate{Input: 4e-10, Output: 4e-10}}
	got, err := EvaluateCost(pricer, CostModeCalculate, "", "tiny", canon.Usage{InputTokens: 1, OutputTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount != 1 {
		t.Fatalf("amount = %d, want one nanodollar", got.Amount)
	}
}
