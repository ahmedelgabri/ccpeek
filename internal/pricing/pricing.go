// Package pricing computes estimated USD cost from token usage
// (docs/v2-plan.md §5.3). Rates come from an embedded snapshot of LiteLLM's
// cross-provider pricing database, refreshed at BUILD time with
// scripts/update-pricing.sh — the snapshot is the only runtime source
// (a runtime refresh is parked until it has a consumer).
//
// Unknown models must stay visible: Lookup reports found=false and callers
// aggregate those tokens as "unpriced" — never silently $0.
package pricing

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

//go:embed snapshot.json
var embedded []byte

// Rate is USD per single token. Cache-write uses the provider's short-TTL
// (5-minute) rate — LiteLLM does not carry Anthropic's 2× 1-hour-TTL write
// rate, so long-TTL cache writes are slightly underestimated.
type Rate struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheWrite float64 `json:"cache_write"`
	CacheRead  float64 `json:"cache_read"`
}

// Cost prices a usage record against this rate.
func (r Rate) Cost(u canon.Usage) float64 {
	return float64(u.InputTokens)*r.Input +
		float64(u.OutputTokens)*r.Output +
		float64(u.CacheWriteTokens)*r.CacheWrite +
		float64(u.CacheReadTokens)*r.CacheRead
}

// Table is a set of model rates plus snapshot provenance.
type Table struct {
	Source    string
	FetchedAt string
	rates     map[string]Rate
}

type snapshot struct {
	Source    string          `json:"source"`
	FetchedAt string          `json:"fetched_at"`
	Models    map[string]Rate `json:"models"`
}

// Embedded parses the built-in snapshot.
func Embedded() (*Table, error) {
	return Parse(embedded)
}

// Parse builds a Table from snapshot JSON (embedded or freshly fetched).
func Parse(data []byte) (*Table, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parsing pricing snapshot: %w", err)
	}
	if len(snap.Models) == 0 {
		return nil, fmt.Errorf("pricing snapshot contains no models")
	}
	rates := make(map[string]Rate, len(snap.Models))
	for model, rate := range snap.Models {
		rates[strings.ToLower(model)] = rate
	}
	return &Table{Source: snap.Source, FetchedAt: snap.FetchedAt, rates: rates}, nil
}

// Len reports how many models the table prices.
func (t *Table) Len() int { return len(t.rates) }

// Lookup resolves a model identifier to a rate, trying the raw id first and
// then progressively normalized candidates (provider prefixes, Bedrock and
// Vertex id shapes). found=false means the model can't be priced.
func (t *Table) Lookup(model string) (rate Rate, found bool) {
	for _, candidate := range Candidates(model) {
		if r, ok := t.rates[candidate]; ok {
			return r, true
		}
	}
	return Rate{}, false
}

var (
	bedrockRegion  = regexp.MustCompile(`^(us|eu|apac|jp|au|ca|global)\.`)
	bedrockVersion = regexp.MustCompile(`-v\d+(:\d+)?$`)
)

// Candidates returns the ordered lookup keys tried for a model identifier.
// Exported for tests and for explaining "why is this model unpriced".
func Candidates(model string) []string {
	seen := make(map[string]bool, 6)
	var out []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}

	base := strings.ToLower(strings.TrimSpace(model))
	add(base)

	// Router/provider prefix: "anthropic/claude-…", "openrouter/anthropic/…"
	// — try each trailing segment.
	if strings.Contains(base, "/") {
		parts := strings.Split(base, "/")
		for i := 1; i < len(parts); i++ {
			add(strings.Join(parts[i:], "/"))
		}
	}
	tail := out[len(out)-1]

	// Vertex date suffix: "claude-sonnet-4-5@20250929".
	if at := strings.IndexByte(tail, '@'); at > 0 {
		add(tail[:at])
	}

	// Bedrock: "us.anthropic.claude-3-5-sonnet-20241022-v2:0".
	noRegion := bedrockRegion.ReplaceAllString(tail, "")
	add(noRegion)
	if vendor, rest, ok := strings.Cut(noRegion, "."); ok && !strings.Contains(vendor, "-") {
		add(rest)
		add(bedrockVersion.ReplaceAllString(rest, ""))
	}
	add(bedrockVersion.ReplaceAllString(noRegion, ""))

	return out
}
