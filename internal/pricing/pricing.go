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
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

//go:embed snapshot.json
var embedded []byte

// AlgorithmVersion changes whenever cost-selection or bucket arithmetic
// changes. It is folded into Table.Fingerprint so materialized rollups are
// invalidated by code semantics as well as by snapshot bytes.
const AlgorithmVersion = "cost-v2-zero-fallback-partial-cache-ttl"

// Rate is USD per single token. Missing cache fields remain distinguishable
// from a genuine zero rate; otherwise a known model with cache traffic would
// silently treat an absent price as free.
type Rate struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheWrite   float64 `json:"cache_write,omitempty"`
	CacheWrite1h float64 `json:"cache_write_1h,omitempty"`
	CacheRead    float64 `json:"cache_read,omitempty"`

	cacheWriteMissing   bool
	cacheWrite1hMissing bool
	cacheReadMissing    bool
}

// UnmarshalJSON preserves field presence while keeping Rate literals concise
// in tests: programmatically constructed rates treat their numeric fields as
// intentional, including intentional zeroes.
func (r *Rate) UnmarshalJSON(data []byte) error {
	var raw struct {
		Input        float64  `json:"input"`
		Output       float64  `json:"output"`
		CacheWrite   *float64 `json:"cache_write"`
		CacheWrite1h *float64 `json:"cache_write_1h"`
		CacheRead    *float64 `json:"cache_read"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Input, r.Output = raw.Input, raw.Output
	r.cacheWriteMissing = raw.CacheWrite == nil
	r.cacheWrite1hMissing = raw.CacheWrite1h == nil
	r.cacheReadMissing = raw.CacheRead == nil
	if raw.CacheWrite != nil {
		r.CacheWrite = *raw.CacheWrite
	}
	if raw.CacheWrite1h != nil {
		r.CacheWrite1h = *raw.CacheWrite1h
	}
	if raw.CacheRead != nil {
		r.CacheRead = *raw.CacheRead
	}
	return nil
}

// Price prices every bucket whose rate is known and returns unpriced tokens
// by bucket. CacheWrite1hTokens is a subset of CacheWriteTokens; malformed
// values are clamped so they can never create negative five-minute writes.
func (r Rate) Price(u canon.Usage) (cost float64, unpriced canon.Usage) {
	cw1h := min(max(u.CacheWrite1hTokens, 0), max(u.CacheWriteTokens, 0))
	cw5m := max(u.CacheWriteTokens, 0) - cw1h
	cost = float64(max(u.InputTokens, 0))*r.Input +
		float64(max(u.OutputTokens, 0))*r.Output
	if r.cacheReadMissing {
		unpriced.CacheReadTokens = max(u.CacheReadTokens, 0)
	} else {
		cost += float64(max(u.CacheReadTokens, 0)) * r.CacheRead
	}
	if r.cacheWriteMissing {
		unpriced.CacheWriteTokens += cw5m
	} else {
		cost += float64(cw5m) * r.CacheWrite
	}
	if r.cacheWrite1hMissing {
		unpriced.CacheWriteTokens += cw1h
		unpriced.CacheWrite1hTokens = cw1h
	} else {
		cost += float64(cw1h) * r.CacheWrite1h
	}
	return cost, unpriced
}

// CacheWriteRate, CacheWrite1hRate, and CacheReadRate expose both value and
// presence for diagnostics and partial-pricing callers.
func (r Rate) CacheWriteRate() (float64, bool)   { return r.CacheWrite, !r.cacheWriteMissing }
func (r Rate) CacheWrite1hRate() (float64, bool) { return r.CacheWrite1h, !r.cacheWrite1hMissing }
func (r Rate) CacheReadRate() (float64, bool)    { return r.CacheRead, !r.cacheReadMissing }

// Cost prices known buckets and is retained for callers that deliberately do
// not need partial-pricing diagnostics.
func (r Rate) Cost(u canon.Usage) float64 {
	cost, _ := r.Price(u)
	return cost
}

// Table is a set of model rates plus snapshot provenance.
type Table struct {
	Source      string
	FetchedAt   string
	fingerprint string
	rates       map[string]Rate
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
	sum := sha256.Sum256(append(append([]byte(AlgorithmVersion), 0), data...))
	return &Table{
		Source: snap.Source, FetchedAt: snap.FetchedAt,
		fingerprint: hex.EncodeToString(sum[:]), rates: rates,
	}, nil
}

// Len reports how many models the table prices.
func (t *Table) Len() int { return len(t.rates) }

// Fingerprint identifies both the exact snapshot bytes and pricing algorithm.
func (t *Table) Fingerprint() string { return t.fingerprint }

// Provenance reports the immutable snapshot source and fetch timestamp.
func (t *Table) Provenance() (source, fetchedAt, algorithm string) {
	return t.Source, t.FetchedAt, AlgorithmVersion
}

// Lookup resolves a model identifier to a rate, trying the raw id first and
// then progressively normalized candidates (provider prefixes, Bedrock and
// Vertex id shapes). found=false means the model can't be priced.
func (t *Table) Lookup(model string) (rate Rate, found bool) {
	rate, _, found = t.Resolve(model)
	return rate, found
}

// Resolve is Lookup with provenance: key is the exact snapshot candidate that
// matched, so diagnostics can explain provider-prefix fallbacks.
func (t *Table) Resolve(model string) (rate Rate, key string, found bool) {
	for _, candidate := range Candidates(model) {
		if r, ok := t.rates[candidate]; ok {
			return r, candidate, true
		}
	}
	return Rate{}, "", false
}

var (
	bedrockRegion  = regexp.MustCompile(`^(us|eu|apac|jp|au|ca|global)\.`)
	bedrockVersion = regexp.MustCompile(`-v\d+(:\d+)?$`)
)

// Candidates returns the ordered lookup keys tried for a model identifier.
// Exported for tests and for explaining "why is this model unpriced".
//
// An empty (or whitespace-only) identifier has no candidates at all:
// messages.model defaults to ” and adapters attach usage independently
// of the model field — a Codex token_count that arrives before the
// turn_context event, a Cursor blob without `model`, an OpenCode message
// with tokens but no modelID — so this is ordinary data, and it must
// resolve to "unpriced", never to a panic on the empty candidate list.
func Candidates(model string) []string {
	base := strings.ToLower(strings.TrimSpace(model))
	if base == "" {
		return nil
	}

	seen := make(map[string]bool, 6)
	var out []string
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}

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
