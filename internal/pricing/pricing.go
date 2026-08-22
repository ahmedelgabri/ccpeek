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
	"sort"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

//go:embed snapshot.json
var embedded []byte

// AlgorithmVersion changes whenever cost-selection or bucket arithmetic
// changes. It is folded into Table.Fingerprint so materialized rollups are
// invalidated by code semantics as well as by snapshot bytes.
const AlgorithmVersion = "cost-v4-fixed-point-effective-tiered-auto"

// Rate is USD per single token. Missing cache fields remain distinguishable
// from a genuine zero rate; otherwise a known model with cache traffic would
// silently treat an absent price as free.
type Rate struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheWrite   float64 `json:"cache_write,omitempty"`
	CacheWrite1h float64 `json:"cache_write_1h,omitempty"`
	CacheRead    float64 `json:"cache_read,omitempty"`
	Tiers        []Tier  `json:"tiers,omitempty"`

	cacheWriteMissing   bool
	cacheWrite1hMissing bool
	cacheReadMissing    bool
}

// Tier overrides whichever base rates a provider changes after a request's
// total input crosses AboveInputTokens. Nil fields inherit the lower tier;
// this matters for providers that increase input/output while retaining their
// base cache-read rate.
type Tier struct {
	AboveInputTokens int64    `json:"above_input_tokens"`
	Input            *float64 `json:"input,omitempty"`
	Output           *float64 `json:"output,omitempty"`
	CacheWrite       *float64 `json:"cache_write,omitempty"`
	CacheWrite1h     *float64 `json:"cache_write_1h,omitempty"`
	CacheRead        *float64 `json:"cache_read,omitempty"`
}

// RateCard is one effective-dated override for a model. EffectiveTo is
// exclusive. Empty bounds are open; overlapping cards resolve to the one with
// the latest start. Price-card producers, not snapshot fetch times, own these
// dates.
type RateCard struct {
	EffectiveFrom string `json:"effective_from,omitempty"`
	EffectiveTo   string `json:"effective_to,omitempty"`
	Source        string `json:"source,omitempty"`
	Rate          Rate   `json:"rate"`
}

// Resolution explains the exact key, historical card, and request tier used.
type Resolution struct {
	Key              string `json:"key"`
	EffectiveFrom    string `json:"effectiveFrom,omitempty"`
	EffectiveTo      string `json:"effectiveTo,omitempty"`
	Source           string `json:"source,omitempty"`
	AboveInputTokens int64  `json:"aboveInputTokens,omitempty"`
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
		Tiers        []Tier   `json:"tiers"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.Input, r.Output, r.Tiers = raw.Input, raw.Output, raw.Tiers
	sort.Slice(r.Tiers, func(i, j int) bool { return r.Tiers[i].AboveInputTokens < r.Tiers[j].AboveInputTokens })
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

// ForInput returns the rate selected for one request and the threshold that
// selected it. Input includes ordinary input, cache reads, and cache writes.
func (r Rate) ForInput(inputTokens int64) (Rate, int64) {
	selected := r
	selected.Tiers = nil
	var threshold int64
	for _, tier := range r.Tiers {
		if inputTokens <= tier.AboveInputTokens {
			break
		}
		threshold = tier.AboveInputTokens
		if tier.Input != nil {
			selected.Input = *tier.Input
		}
		if tier.Output != nil {
			selected.Output = *tier.Output
		}
		if tier.CacheWrite != nil {
			selected.CacheWrite, selected.cacheWriteMissing = *tier.CacheWrite, false
		}
		if tier.CacheWrite1h != nil {
			selected.CacheWrite1h, selected.cacheWrite1hMissing = *tier.CacheWrite1h, false
		}
		if tier.CacheRead != nil {
			selected.CacheRead, selected.cacheReadMissing = *tier.CacheRead, false
		}
	}
	return selected, threshold
}

// PriceAmount prices every bucket whose rate is known using fixed-point
// nanodollars and returns unpriced tokens by bucket. CacheWrite1hTokens is a
// subset of CacheWriteTokens; malformed values are clamped so they can never
// create negative five-minute writes.
func (r Rate) PriceAmount(u canon.Usage) (Amount, canon.Usage, error) {
	cw1h := min(max(u.CacheWrite1hTokens, 0), max(u.CacheWriteTokens, 0))
	cw5m := max(u.CacheWriteTokens, 0) - cw1h
	parts := []tokenRate{
		{tokens: max(u.InputTokens, 0), usdPerToken: r.Input},
		{tokens: max(u.OutputTokens, 0), usdPerToken: r.Output},
	}
	var unpriced canon.Usage
	if r.cacheReadMissing {
		unpriced.CacheReadTokens = max(u.CacheReadTokens, 0)
	} else {
		parts = append(parts, tokenRate{tokens: max(u.CacheReadTokens, 0), usdPerToken: r.CacheRead})
	}
	if r.cacheWriteMissing {
		unpriced.CacheWriteTokens += cw5m
	} else {
		parts = append(parts, tokenRate{tokens: cw5m, usdPerToken: r.CacheWrite})
	}
	if r.cacheWrite1hMissing {
		unpriced.CacheWriteTokens += cw1h
		unpriced.CacheWrite1hTokens = cw1h
	} else {
		parts = append(parts, tokenRate{tokens: cw1h, usdPerToken: r.CacheWrite1h})
	}
	cost, err := amountFromTokenRates(parts...)
	return cost, unpriced, err
}

// Price is the floating-point compatibility wrapper. All internal aggregation
// should use PriceAmount and convert only at an API boundary.
func (r Rate) Price(u canon.Usage) (cost float64, unpriced canon.Usage) {
	amount, unpriced, err := r.PriceAmount(u)
	if err != nil {
		return 0, unpriced
	}
	return amount.USD(), unpriced
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
	history     map[string][]RateCard
}

type snapshot struct {
	Source    string                `json:"source"`
	FetchedAt string                `json:"fetched_at"`
	Models    map[string]Rate       `json:"models"`
	History   map[string][]RateCard `json:"history,omitempty"`
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
	history := make(map[string][]RateCard, len(snap.History))
	for model, cards := range snap.History {
		key := strings.ToLower(model)
		sort.SliceStable(cards, func(i, j int) bool { return cards[i].EffectiveFrom < cards[j].EffectiveFrom })
		history[key] = cards
	}
	sum := sha256.Sum256(append(append([]byte(AlgorithmVersion), 0), data...))
	return &Table{
		Source: snap.Source, FetchedAt: snap.FetchedAt,
		fingerprint: hex.EncodeToString(sum[:]), rates: rates, history: history,
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

// ResolveAt selects an effective-dated card and request-size tier. If a model
// has historical cards, a dated request in a gap is deliberately unpriced
// rather than silently receiving today's rate. Undated callers retain current
// snapshot behavior.
func (t *Table) ResolveAt(model string, at time.Time, inputTokens int64) (Rate, Resolution, bool) {
	candidates := Candidates(model)
	var current Rate
	var currentKey string
	var currentFound bool
	for _, candidate := range candidates {
		if rate, ok := t.rates[candidate]; ok {
			current, currentKey, currentFound = rate, candidate, true
			break
		}
	}
	if at.IsZero() {
		if !currentFound {
			return Rate{}, Resolution{}, false
		}
		selected, threshold := current.ForInput(inputTokens)
		return selected, Resolution{Key: currentKey, AboveInputTokens: threshold}, true
	}

	// History and current rates are normalized independently. A provider-
	// specific current key must not prevent a less-specific historical card
	// from governing the same normalized model.
	for _, candidate := range candidates {
		cards := t.history[candidate]
		if len(cards) == 0 {
			continue
		}
		card, ok := effectiveCard(cards, at)
		if !ok {
			// Historical coverage exists for this normalized model, so a gap is
			// authoritative. Do not conceal it with today's current rate.
			return Rate{}, Resolution{Key: candidate}, false
		}
		cardRate := card.Rate
		if len(cardRate.Tiers) == 0 && currentFound {
			// Sparse historical cards inherit the current card's absolute tier
			// rates. This deliberately mixes eras when base prices changed;
			// producers must include card-specific tiers to avoid that assumption.
			cardRate.Tiers = current.Tiers
		}
		selected, threshold := cardRate.ForInput(inputTokens)
		return selected, Resolution{
			Key: candidate, EffectiveFrom: card.EffectiveFrom,
			EffectiveTo: card.EffectiveTo, Source: card.Source,
			AboveInputTokens: threshold,
		}, true
	}

	if !currentFound {
		return Rate{}, Resolution{}, false
	}
	selected, threshold := current.ForInput(inputTokens)
	return selected, Resolution{Key: currentKey, AboveInputTokens: threshold}, true
}

func effectiveCard(cards []RateCard, at time.Time) (RateCard, bool) {
	var selected RateCard
	found := false
	for _, card := range cards {
		from, fromOK := parseBoundary(card.EffectiveFrom)
		to, toOK := parseBoundary(card.EffectiveTo)
		if card.EffectiveFrom != "" && (!fromOK || at.Before(from)) {
			continue
		}
		if card.EffectiveTo != "" && (!toOK || !at.Before(to)) {
			continue
		}
		if !found || card.EffectiveFrom >= selected.EffectiveFrom {
			selected, found = card, true
		}
	}
	return selected, found
}

func parseBoundary(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, true
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
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
