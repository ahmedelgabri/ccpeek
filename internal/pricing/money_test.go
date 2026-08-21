package pricing

import (
	"math"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func TestAmountRoundTrip(t *testing.T) {
	for _, tt := range []struct {
		usd  float64
		want string
	}{
		{0, "0"},
		{0.0142, "0.0142"},
		{12.3456789014, "12.345678901"},
		{12.3456789015, "12.345678902"},
		{-0.000000001, "-0.000000001"},
	} {
		got, err := AmountFromUSD(tt.usd)
		if err != nil {
			t.Fatalf("AmountFromUSD(%v): %v", tt.usd, err)
		}
		if got.String() != tt.want {
			t.Errorf("AmountFromUSD(%v) = %q, want %q", tt.usd, got.String(), tt.want)
		}
	}
}

func TestAmountRejectsInvalidAndOverflow(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), 1e20} {
		if _, err := AmountFromUSD(value); err == nil {
			t.Errorf("AmountFromUSD(%v) unexpectedly succeeded", value)
		}
	}
	if _, err := Amount(math.MaxInt64).Add(1); err == nil {
		t.Error("overflowing Add unexpectedly succeeded")
	}
}

func TestPriceAmountCombinesBeforeRounding(t *testing.T) {
	// Each bucket is 0.4 nanodollars and would round to zero separately. The
	// combined row is 0.8 nanodollars and therefore rounds to one.
	rate := Rate{Input: 4e-10, Output: 4e-10}
	got, unpriced, err := rate.PriceAmount(canon.Usage{InputTokens: 1, OutputTokens: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 || got.String() != "0.000000001" {
		t.Errorf("cost = %d (%s), want one nanodollar", got, got.String())
	}
	if unpriced.InputTokens != 0 || unpriced.OutputTokens != 0 {
		t.Errorf("unpriced = %+v", unpriced)
	}
}

func TestPriceAmountPreservesSubNanoTokenRates(t *testing.T) {
	rate := Rate{Input: 1.3e-10}
	got, _, err := rate.PriceAmount(canon.Usage{InputTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.000000001" {
		t.Errorf("cost = %s, want 0.000000001", got.String())
	}
}
