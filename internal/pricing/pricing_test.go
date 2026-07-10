package pricing

import (
	"math"
	"reflect"
	"testing"

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
	// A long-stable key that should survive snapshot refreshes.
	rate, ok := tbl.Lookup("claude-3-opus-20240229")
	if !ok {
		t.Fatal("claude-3-opus-20240229 not in embedded snapshot")
	}
	if rate.Input <= 0 || rate.Output <= 0 {
		t.Errorf("suspicious rate for stable model: %+v", rate)
	}
}
