package pricing

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func syntheticTable(t *testing.T) *Table {
	t.Helper()
	tbl, err := Parse([]byte(`{
		"source": "test",
		"fetched_at": "2026-07-10T00:00:00Z",
		"models": {
			"claude-sonnet-5": {"input": 3e-6, "output": 15e-6, "cache_write": 3.75e-6, "cache_read": 3e-7},
			"claude-3-5-sonnet-20241022": {"input": 3e-6, "output": 15e-6, "cache_write": 3.75e-6, "cache_read": 3e-7},
			"gpt-5.2-codex": {"input": 1.25e-6, "output": 1e-5, "cache_write": 0, "cache_read": 1.25e-7}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return tbl
}

func TestLookupShapes(t *testing.T) {
	tbl := syntheticTable(t)
	for _, id := range []string{
		"claude-sonnet-5",                      // exact
		"Claude-Sonnet-5",                      // case
		"anthropic/claude-sonnet-5",            // provider prefix
		"openrouter/anthropic/claude-sonnet-5", // nested router prefix
		"claude-sonnet-5@20260203",             // vertex date suffix
	} {
		if _, ok := tbl.Lookup(id); !ok {
			t.Errorf("Lookup(%q) not found; candidates tried: %v", id, Candidates(id))
		}
	}

	for _, id := range []string{
		"us.anthropic.claude-3-5-sonnet-20241022-v2:0", // bedrock, region + version
		"anthropic.claude-3-5-sonnet-20241022-v2:0",    // bedrock, no region
	} {
		if _, ok := tbl.Lookup(id); !ok {
			t.Errorf("Lookup(%q) not found; candidates tried: %v", id, Candidates(id))
		}
	}

	if _, ok := tbl.Lookup("totally-unknown-model"); ok {
		t.Error("unknown model must report found=false, not a zero rate")
	}
}

func TestCost(t *testing.T) {
	tbl := syntheticTable(t)
	rate, ok := tbl.Lookup("claude-sonnet-5")
	if !ok {
		t.Fatal("lookup failed")
	}
	got := rate.Cost(canon.Usage{
		InputTokens:      1_000_000,
		OutputTokens:     100_000,
		CacheWriteTokens: 200_000,
		CacheReadTokens:  5_000_000,
	})
	// 1M×3e-6 + 100k×15e-6 + 200k×3.75e-6 + 5M×3e-7 = 3 + 1.5 + 0.75 + 1.5
	want := 6.75
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("Cost = %v, want %v", got, want)
	}
}

func TestPartialCachePricingAndOneHourTTL(t *testing.T) {
	tbl, err := Parse([]byte(`{
		"source":"test","fetched_at":"2026-08-21T00:00:00Z",
		"models":{
			"full":{"input":1,"output":2,"cache_write":3,"cache_write_1h":4,"cache_read":5},
			"partial":{"input":1,"output":2}
		}}
	`))
	if err != nil {
		t.Fatal(err)
	}
	u := canon.Usage{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3,
		CacheWriteTokens: 11, CacheWrite1hTokens: 7,
	}
	full, _ := tbl.Lookup("full")
	if cost, unpriced := full.Price(u); cost != 1+4+15+12+28 ||
		unpriced != (canon.Usage{}) {
		t.Errorf("full price = %v, unpriced %+v", cost, unpriced)
	}
	partial, _ := tbl.Lookup("partial")
	if cost, unpriced := partial.Price(u); cost != 5 ||
		unpriced.CacheReadTokens != 3 || unpriced.CacheWriteTokens != 11 {
		t.Errorf("partial price = %v, unpriced %+v; want cost 5, cache read/write 3/11", cost, unpriced)
	}
}

func TestFingerprintCoversSnapshotAndAlgorithm(t *testing.T) {
	a := syntheticTable(t)
	b := syntheticTable(t)
	if a.Fingerprint() == "" || a.Fingerprint() != b.Fingerprint() {
		t.Fatalf("stable fingerprint = %q / %q", a.Fingerprint(), b.Fingerprint())
	}
	changed, err := Parse([]byte(`{"source":"test","fetched_at":"different","models":{"m":{"input":1,"output":1}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint() == a.Fingerprint() {
		t.Error("different snapshot has the same fingerprint")
	}
}

func TestCandidatesOrder(t *testing.T) {
	got := Candidates("us.anthropic.claude-3-5-sonnet-20241022-v2:0")
	want := []string{
		"us.anthropic.claude-3-5-sonnet-20241022-v2:0",
		"anthropic.claude-3-5-sonnet-20241022-v2:0",
		"claude-3-5-sonnet-20241022-v2:0",
		"claude-3-5-sonnet-20241022",
		"anthropic.claude-3-5-sonnet-20241022",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Candidates = %v, want %v", got, want)
	}
}

func TestTierSelection(t *testing.T) {
	baseInput, tierInput, tierOutput := 1.0, 2.0, 3.0
	rate := Rate{Input: baseInput, Output: 1, Tiers: []Tier{{
		AboveInputTokens: 200_000, Input: &tierInput, Output: &tierOutput,
	}}}
	if got, threshold := rate.ForInput(200_000); got.Input != 1 || threshold != 0 {
		t.Errorf("boundary rate = %+v at %d", got, threshold)
	}
	if got, threshold := rate.ForInput(200_001); got.Input != 2 || got.Output != 3 || threshold != 200_000 {
		t.Errorf("tier rate = %+v at %d", got, threshold)
	}
}

func TestEffectiveDatedCardsAndCoverageGaps(t *testing.T) {
	tbl, err := Parse([]byte(`{
		"source":"current","fetched_at":"2026-08-21T00:00:00Z",
		"models":{"model":{"input":9,"output":9}},
		"history":{"model":[
			{"effective_from":"2025-01-01","effective_to":"2026-01-01","source":"vendor-2025","rate":{"input":1,"output":2}},
			{"effective_from":"2026-02-01","effective_to":"2027-01-01","source":"vendor-2026","rate":{"input":3,"output":4}}
		]}}
	`))
	if err != nil {
		t.Fatal(err)
	}
	at := func(value string) time.Time {
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	if rate, resolution, ok := tbl.ResolveAt("model", at("2025-06-01"), 1); !ok || rate.Input != 1 || resolution.Source != "vendor-2025" {
		t.Errorf("2025 resolution = %+v, %+v, %v", rate, resolution, ok)
	}
	if _, resolution, ok := tbl.ResolveAt("model", at("2026-01-15"), 1); ok || resolution.Key != "model" {
		t.Errorf("coverage gap unexpectedly resolved: %+v, %v", resolution, ok)
	}
	if rate, resolution, ok := tbl.ResolveAt("model", time.Time{}, 1); !ok || rate.Input != 9 || resolution.EffectiveFrom != "" {
		t.Errorf("undated current resolution = %+v, %+v, %v", rate, resolution, ok)
	}
}

func TestEmbeddedSnapshot(t *testing.T) {
	tbl, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if tbl.Len() < 1000 {
		t.Fatalf("embedded snapshot has %d models — regenerate with scripts/update-pricing.sh", tbl.Len())
	}
	if tbl.Source == "" || tbl.FetchedAt == "" {
		t.Error("embedded snapshot missing provenance")
	}
	var tiered int
	for _, rate := range tbl.rates {
		if len(rate.Tiers) > 0 {
			tiered++
		}
	}
	if tiered == 0 {
		t.Error("embedded snapshot contains no long-context tiers")
	}
	// A long-stable key that should survive snapshot refreshes.
	rate, ok := tbl.Lookup("claude-3-opus-20240229")
	if !ok {
		t.Fatal("claude-3-opus-20240229 not in embedded snapshot")
	}
	if rate.Input <= 0 || rate.Output <= 0 {
		t.Errorf("suspicious rate for stable model: %+v", rate)
	}
}

// A message can legitimately carry usage with no model: Codex attaches a
// token_count delta before the turn_context event names the model, Cursor
// blobs may omit it, and messages.model defaults to ”. Lookup must report
// "unpriced" for those rather than indexing an empty candidate list.
func TestBlankModelIsUnpricedNotFatal(t *testing.T) {
	tbl, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	for _, model := range []string{"", " ", "\t", "\n  \t"} {
		if got := Candidates(model); len(got) != 0 {
			t.Errorf("Candidates(%q) = %v, want no candidates", model, got)
		}
		rate, ok := tbl.Lookup(model)
		if ok {
			t.Errorf("Lookup(%q) reported priced (%+v), want unpriced", model, rate)
		}
		if !reflect.DeepEqual(rate, Rate{}) {
			t.Errorf("Lookup(%q) rate = %+v, want zero value", model, rate)
		}
	}
}
