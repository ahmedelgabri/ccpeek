package pricing

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Amount is an exact fixed-point USD amount in nanodollars. The range is
// roughly ±$9.2 billion; rate multiplication uses arbitrary-precision
// intermediates and reports overflow rather than wrapping.
type Amount int64

const (
	NanosPerUSD  int64 = 1_000_000_000
	picosPerUSD  int64 = 1_000_000_000_000
	picosPerNano int64 = picosPerUSD / NanosPerUSD
)

var (
	bigPicosPerNano = big.NewInt(picosPerNano)
	bigOne          = big.NewInt(1)
)

// AmountFromUSD converts a compatibility float at the system boundary to the
// exact internal representation, rounding once to the nearest nanodollar.
func AmountFromUSD(v float64) (Amount, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid USD amount %v", v)
	}
	if v > float64(math.MaxInt64)/float64(NanosPerUSD) || v < float64(math.MinInt64)/float64(NanosPerUSD) {
		return 0, fmt.Errorf("USD amount out of range: %v", v)
	}
	return Amount(math.Round(v * float64(NanosPerUSD))), nil
}

// MustAmountFromUSD is intended for fixed test fixtures and built-in rates.
func MustAmountFromUSD(v float64) Amount {
	a, err := AmountFromUSD(v)
	if err != nil {
		panic(err)
	}
	return a
}

// USD returns the compatibility floating-point representation used by the v1
// API. Arithmetic and aggregation must happen before this conversion.
func (a Amount) USD() float64 { return float64(a) / float64(NanosPerUSD) }

// String returns a non-exponent decimal USD value with up to nine fractional
// digits, suitable for exact API fields and SQLite diagnostics.
func (a Amount) String() string {
	neg := a < 0
	var magnitude uint64
	if neg {
		magnitude = uint64(-(a + 1)) + 1
	} else {
		magnitude = uint64(a)
	}
	whole := magnitude / uint64(NanosPerUSD)
	fraction := magnitude % uint64(NanosPerUSD)
	s := strconv.FormatUint(whole, 10)
	if fraction != 0 {
		s += "." + strings.TrimRight(fmt.Sprintf("%09d", fraction), "0")
	}
	if neg {
		return "-" + s
	}
	return s
}

// Add reports overflow instead of allowing exact accounting to wrap.
func (a Amount) Add(b Amount) (Amount, error) {
	if (b > 0 && a > Amount(math.MaxInt64)-b) || (b < 0 && a < Amount(math.MinInt64)-b) {
		return 0, fmt.Errorf("USD amount overflow: %s + %s", a.String(), b.String())
	}
	return a + b, nil
}

// amountFromTokenRates multiplies each token count by a USD-per-token rate,
// quantized to picodollars per token, then rounds the combined row once to the
// nearest nanodollar. Combining before rounding avoids per-bucket drift.
func amountFromTokenRates(parts ...tokenRate) (Amount, error) {
	totalPicos := new(big.Int)
	for _, part := range parts {
		if part.tokens <= 0 || part.usdPerToken == 0 {
			continue
		}
		ratePicos := math.Round(part.usdPerToken * float64(picosPerUSD))
		if ratePicos > float64(math.MaxInt64) || ratePicos < float64(math.MinInt64) {
			return 0, fmt.Errorf("token rate out of range: %v", part.usdPerToken)
		}
		product := new(big.Int).Mul(big.NewInt(part.tokens), big.NewInt(int64(ratePicos)))
		totalPicos.Add(totalPicos, product)
	}

	neg := totalPicos.Sign() < 0
	magnitude := new(big.Int).Abs(totalPicos)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(magnitude, bigPicosPerNano, remainder)
	if new(big.Int).Lsh(remainder, 1).Cmp(bigPicosPerNano) >= 0 {
		quotient.Add(quotient, bigOne)
	}
	if neg {
		quotient.Neg(quotient)
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("calculated USD amount out of range")
	}
	return Amount(quotient.Int64()), nil
}

type tokenRate struct {
	tokens      int64
	usdPerToken float64
}
