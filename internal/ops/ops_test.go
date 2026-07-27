package ops

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// newService opens an empty store; ingest is the caller's business.
func newService(t *testing.T) *query.Service {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	return query.New(store, table)
}

// newFixtureService ingests the Claude fixture corpus, which the search
// assertions need real hits from.
func newFixtureService(t *testing.T) *query.Service {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs("../../testdata/agents/claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.New(store, table, claude.New()).Run(context.Background(), ingest.Options{
		ConfigRoots: map[canon.AgentSlug][]string{claude.Slug: {fixtures}},
		Getenv:      func(string) string { return "" },
		Home:        "/nonexistent",
	}); err != nil {
		t.Fatal(err)
	}
	return query.New(store, table)
}

// argsFor fills an op's required inputs with well-shaped values (they
// need not resolve — validation runs before any lookup) plus one
// override.
func argsFor(op Op, name string, value int) Args {
	a := Args{Str: map[string]string{}, Int: map[string]int{}, Bool: map[string]bool{}}
	for _, p := range op.Params {
		if p.Required && p.Type == "string" {
			a.Str[p.Name] = "x"
		}
	}
	a.Int[name] = value
	return a
}

func paramOf(op Op, name string) *Param {
	for _, p := range op.Params {
		if p.Name == name {
			return &p
		}
	}
	return nil
}

// TestRegistryShape pins the operation surface: every op the transports
// derive from must exist with the parameters the review found missing
// from MCP (sessions.model, transcript.full, usage.model), and the read
// surface must match HTTP's endpoint set.
func TestRegistryShape(t *testing.T) {
	byName := map[string]Op{}
	for _, op := range Registry() {
		byName[op.Name] = op
	}

	want := []string{
		"sessions", "session", "transcript", "usage", "search",
		"commands", "history", "stats", "blocks", "scan", "scan-rules",
		"artifacts", "artifact-kinds", "artifact", "tools", "tool", "budget",
	}
	for _, name := range want {
		if _, found := byName[name]; !found {
			t.Errorf("registry missing op %q", name)
		}
	}
	if len(byName) != len(want) {
		t.Errorf("registry has %d ops, want %d — update the HTTP parity expectations too", len(byName), len(want))
	}

	param := func(op, name string) *Param {
		for _, p := range byName[op].Params {
			if p.Name == name {
				return &p
			}
		}
		return nil
	}
	// The drift MCP had: these must be first-class parameters everywhere.
	if param("sessions", "model") == nil {
		t.Error("sessions op lost its model filter")
	}
	if param("transcript", "full") == nil {
		t.Error("transcript op lost its full switch")
	}
	if param("usage", "model") == nil {
		t.Error("usage op lost its model filter")
	}
	for _, op := range Registry() {
		if op.Run == nil {
			t.Errorf("op %q has no executor", op.Name)
		}
		for _, p := range op.Params {
			switch p.Type {
			case "string", "integer", "boolean":
			default:
				t.Errorf("op %q param %q has unknown type %q", op.Name, p.Name, p.Type)
			}
		}
	}
}

// TestLimitPolicyIsDeclaredWhereItIsEnforced: the page-size numbers a
// transport advertises are the numbers the read applies. They were
// declared nowhere — Param.Default existed for exactly one op and could
// not hold an integer — while four ops silently truncated an over-cap
// request, so an agent that asked for 2000 transcript entries got 1000,
// exit 0, and no way to know 300 were missing.
func TestLimitPolicyIsDeclaredWhereItIsEnforced(t *testing.T) {
	// The pinned policy. Changing a limit is a contract change and must
	// be a deliberate edit here as well as in query.
	want := map[string]struct{ def, max int }{
		"sessions":   {50, 500},
		"transcript": {200, 1000},
		"search":     {20, 100},
		"commands":   {100, 1000},
		"history":    {100, 0},
		"blocks":     {24, 200},
		"artifacts":  {100, 0},
		"tools":      {0, 0},
		"usage":      {0, 0},
	}

	svc := newService(t)
	ctx := context.Background()
	seen := map[string]bool{}
	for _, op := range Registry() {
		p := paramOf(op, "limit")
		if p == nil {
			continue
		}
		spec, declared := want[op.Name]
		if !declared {
			t.Errorf("op %q gained a limit parameter with no pinned policy", op.Name)
			continue
		}
		seen[op.Name] = true

		if spec.def == 0 {
			if p.Default != nil {
				t.Errorf("%s: limit default = %v, want none (the op answers in full)", op.Name, p.Default)
			}
		} else if p.Default != spec.def {
			t.Errorf("%s: declared limit default = %v, want %d", op.Name, p.Default, spec.def)
		}
		if p.Max != spec.max {
			t.Errorf("%s: declared limit max = %d, want %d", op.Name, p.Max, spec.max)
		}
		// The prose carries the same numbers, for the transports that show
		// descriptions rather than schema keywords.
		for _, n := range []int{spec.def, spec.max} {
			if n > 0 && !strings.Contains(p.Desc, strconv.Itoa(n)) {
				t.Errorf("%s: limit description %q does not state %d", op.Name, p.Desc, n)
			}
		}
		if spec.max == 0 {
			continue
		}

		// Over the ceiling is a refusal that names the ceiling, so a model
		// can correct the call itself.
		_, _, err := op.Run(ctx, svc, argsFor(op, "limit", spec.max+1))
		if !errors.Is(err, query.ErrBadRequest) {
			t.Errorf("%s: limit=%d gave err %v, want ErrBadRequest", op.Name, spec.max+1, err)
		} else if !strings.Contains(err.Error(), strconv.Itoa(spec.max)) {
			t.Errorf("%s: refusal does not name the maximum %d: %v", op.Name, spec.max, err)
		}

		// The ceiling itself stays valid (the UI's transcript page is
		// exactly the transcript maximum).
		if _, _, err := op.Run(ctx, svc, argsFor(op, "limit", spec.max)); errors.Is(err, query.ErrBadRequest) {
			t.Errorf("%s: limit=%d (the declared maximum) was refused: %v", op.Name, spec.max, err)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("op %q lost its limit parameter", name)
		}
	}
}

// TestEnvelopeEncodesEmptyListsAsArrays: a list op that matched nothing
// is "data": [] on EVERY transport. The CLI and MCP emitted null —
// inconsistently, since whether the query happened to allocate decided
// it — and `jq '.data[]'` errors on null.
func TestEnvelopeEncodesEmptyListsAsArrays(t *testing.T) {
	svc := newService(t)
	ctx := context.Background()
	checked := 0
	for _, op := range Registry() {
		data, empty, err := op.Run(ctx, svc, argsFor(op, "limit", 0))
		if err != nil {
			continue // detail ops need a real entity; the list ops are the subject
		}
		if !empty {
			continue
		}
		checked++
		encoded, err := json.Marshal(Wrap(data))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"data":null`) {
			t.Errorf("op %q encodes an empty result as null: %s", op.Name, encoded)
		}
		if !strings.Contains(string(encoded), `"data":[]`) {
			t.Errorf("op %q empty result = %s, want \"data\":[]", op.Name, encoded)
		}
	}
	if checked < 6 {
		t.Errorf("only %d list ops answered empty; the test stopped covering the contract", checked)
	}
}

// TestSearchSnippetsCarryTheAgentMarker: the FTS delimiters are control
// characters because indexed content can contain any printable text (the
// web UI's highlighter splits on them), but they reach a terminal or a
// model as escape noise. The AGENT transports get a readable marker; the
// query layer — and so the HTTP/UI path — keeps the control characters.
func TestSearchSnippetsCarryTheAgentMarker(t *testing.T) {
	svc := newFixtureService(t)
	ctx := context.Background()
	var search Op
	for _, op := range Registry() {
		if op.Name == "search" {
			search = op
		}
	}

	a := Args{Str: map[string]string{"query": "rate limiting"}, Int: map[string]int{}, Bool: map[string]bool{}}
	data, empty, err := search.Run(ctx, svc, a)
	if err != nil || empty {
		t.Fatalf("search: err %v, empty %v", err, empty)
	}
	hits := data.([]query.SearchHit)
	marked := false
	for _, h := range hits {
		if strings.ContainsAny(h.Snippet, query.SnippetOpen+query.SnippetClose) {
			t.Errorf("agent snippet still carries a control character: %q", h.Snippet)
		}
		if strings.Contains(h.Snippet, SnippetMarker) {
			marked = true
		}
	}
	if !marked {
		t.Errorf("no snippet marks its match with %q: %+v", SnippetMarker, hits)
	}

	// The query layer is untouched: the UI needs the control characters.
	raw, err := svc.Search(ctx, "rate limiting", query.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, h := range raw {
		if strings.Contains(h.Snippet, query.SnippetOpen) {
			found = true
		}
	}
	if !found {
		t.Error("the query layer stopped emitting the UI's snippet delimiters")
	}
}
