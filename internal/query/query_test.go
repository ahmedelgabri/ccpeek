package query

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/codex"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/opencode"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/pi"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

const (
	claudeSession1 = "11111111-aaaa-bbbb-cccc-111111111111"
	claudeSession3 = "33333333-aaaa-bbbb-cccc-333333333333"
	piMain         = "9f8e7d6c-1111-2222-3333-444455556666"
	piFork         = "1a2b3c4d-7777-8888-9999-000011112222"
)

// newService ingests the fixture corpus once per test into a fresh store.
func newService(t *testing.T) *Service {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := func(dir string) []string {
		p, err := filepath.Abs(filepath.Join("../../testdata/agents", dir))
		if err != nil {
			t.Fatal(err)
		}
		return []string{p}
	}
	runner := ingest.New(store, table, claude.New(), pi.New())
	if _, err := runner.Run(context.Background(), ingest.Options{
		ConfigRoots: map[canon.AgentSlug][]string{
			claude.Slug: fixtures("claude-code"),
			pi.Slug:     fixtures("pi"),
		},
		Getenv: func(string) string { return "" },
		Home:   "/nonexistent",
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return New(store, table)
}

func TestSessionsList(t *testing.T) {
	s := newService(t)
	sessions, err := s.Sessions(context.Background(), SessionsFilter{})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	if len(sessions) != 5 {
		t.Fatalf("sessions = %d, want 5", len(sessions))
	}
	// Newest-first by modified_at: claude session 3 (2026-07-03) first.
	if sessions[0].ID != claudeSession3 {
		t.Errorf("first session = %s, want %s", sessions[0].ID, claudeSession3)
	}

	// Real token totals on the earliest claude session.
	var s1 *SessionSummary
	for i := range sessions {
		if sessions[i].ID == claudeSession1 {
			s1 = &sessions[i]
		}
	}
	if s1 == nil {
		t.Fatal("session 1 missing")
	}
	if s1.Tokens.Input != 26 || s1.Tokens.Output != 317 ||
		s1.Tokens.CacheWrite != 2550 || s1.Tokens.CacheRead != 4950 {
		t.Errorf("session1 tokens = %+v", s1.Tokens)
	}
	if s1.CostUSD <= 0 {
		t.Errorf("session1 cost = %v, want > 0", s1.CostUSD)
	}
	if s1.Messages != 7 || s1.ToolCalls != 2 {
		t.Errorf("session1 counts = %d msgs / %d tools", s1.Messages, s1.ToolCalls)
	}
}

func TestSessionsFilters(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	piOnly, err := s.Sessions(ctx, SessionsFilter{Agent: "pi"})
	if err != nil || len(piOnly) != 2 {
		t.Fatalf("pi sessions = %d (err %v), want 2", len(piOnly), err)
	}

	proj, err := s.Sessions(ctx, SessionsFilter{Project: "/home/u/demo/api"})
	if err != nil || len(proj) != 5 {
		t.Fatalf("project sessions = %d (err %v), want 5", len(proj), err)
	}

	since, err := s.Sessions(ctx, SessionsFilter{Since: "2026-07-03"})
	if err != nil || len(since) != 1 {
		t.Fatalf("since sessions = %d (err %v), want 1", len(since), err)
	}

	titled, err := s.Sessions(ctx, SessionsFilter{Query: "rate limiting"})
	if err != nil || len(titled) == 0 {
		t.Fatalf("title filter = %d (err %v), want >0", len(titled), err)
	}
}

func TestSessionDetail(t *testing.T) {
	s := newService(t)
	detail, err := s.Session(context.Background(), "claude-code", claudeSession1)
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	// Linked artifacts: todo (filename uuid), task group (id match), facet.
	kinds := map[string]bool{}
	for _, a := range detail.Artifacts {
		kinds[a.Kind] = true
	}
	for _, want := range []string{"todo_list", "task_group", "usage_facet"} {
		if !kinds[want] {
			t.Errorf("missing linked artifact %s (have %v)", want, detail.Artifacts)
		}
	}
	if len(detail.Models) != 2 {
		t.Errorf("models = %v, want 2 (sonnet + haiku)", detail.Models)
	}

	// The unpriced sidechain model shows up as unpriced tokens on session 3.
	d3, err := s.Session(context.Background(), "claude-code", claudeSession3)
	if err != nil {
		t.Fatal(err)
	}
	if d3.UnpricedTokens == 0 {
		t.Error("session3 unpriced tokens = 0, want > 0 (experimental model)")
	}

	// Pi fork relation is visible from both ends.
	pf, err := s.Session(context.Background(), "pi", piFork)
	if err != nil {
		t.Fatal(err)
	}
	if len(pf.Relations) != 1 || pf.Relations[0].Kind != "fork_of" ||
		pf.Relations[0].Direction != "out" || pf.Relations[0].SessionID != piMain {
		t.Errorf("fork relations = %+v", pf.Relations)
	}
	pm, err := s.Session(context.Background(), "pi", piMain)
	if err != nil {
		t.Fatal(err)
	}
	if len(pm.Relations) != 1 || pm.Relations[0].Direction != "in" {
		t.Errorf("main relations = %+v", pm.Relations)
	}

	if _, err := s.Session(context.Background(), "claude-code", "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing session err = %v, want ErrNotFound", err)
	}
}

func TestTranscript(t *testing.T) {
	s := newService(t)
	msgs, err := s.Transcript(context.Background(), "claude-code", claudeSession1, TranscriptOptions{})
	if err != nil {
		t.Fatalf("Transcript: %v", err)
	}
	if len(msgs) != 7 {
		t.Fatalf("transcript entries = %d, want 7", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Text == "" {
		t.Errorf("first entry = %+v", msgs[0])
	}
	if msgs[0].Content != "" {
		t.Error("content included without Full option")
	}

	// Range + full payload.
	tail, err := s.Transcript(context.Background(), "claude-code", claudeSession1,
		TranscriptOptions{FromSeq: 5, Limit: 10, Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 {
		t.Fatalf("tail = %d entries, want 2", len(tail))
	}
	if tail[0].Seq != 5 || tail[0].Content == "" {
		t.Errorf("tail[0] = %+v", tail[0])
	}
}

func TestUsageAggregates(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	byModel, err := s.Usage(ctx, UsageFilter{GroupBy: "model"})
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	groups := map[string]UsageRow{}
	for _, r := range byModel {
		groups[r.Group] = r
	}
	if _, ok := groups["claude-sonnet-5"]; !ok {
		t.Errorf("no sonnet group: %v", groups)
	}
	if g := groups["experimental-audit-model"]; !g.HasUnpriced {
		t.Errorf("experimental model not flagged unpriced: %+v", g)
	}

	byAgent, err := s.Usage(ctx, UsageFilter{GroupBy: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byAgent) != 2 {
		t.Fatalf("agent groups = %d, want 2", len(byAgent))
	}

	byDay, err := s.Usage(ctx, UsageFilter{GroupBy: "day", Since: "2026-07-01", Until: "2026-07-02"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byDay) != 1 || byDay[0].Group != "2026-07-01" {
		t.Errorf("day groups = %+v, want exactly 2026-07-01", byDay)
	}

	if _, err := s.Usage(ctx, UsageFilter{GroupBy: "bogus"}); err == nil {
		t.Error("bogus group accepted")
	}
}

func TestSearch(t *testing.T) {
	s := newService(t)
	hits, err := s.Search(context.Background(), "rate limiting", SearchFilter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	var messageHit, artifactHit bool
	for _, h := range hits {
		if h.DocType == "message" && h.SessionID != "" {
			messageHit = true
		}
		if h.DocType == "plan" && h.Artifact != "" {
			artifactHit = true
		}
		if h.Snippet == "" {
			t.Errorf("hit without snippet: %+v", h)
		}
	}
	if !messageHit {
		t.Error("no message hit resolving to a session")
	}
	if !artifactHit {
		t.Error("no plan artifact hit")
	}

	// Operators in user input must not break MATCH.
	if _, err := s.Search(context.Background(), `AND OR "unbalanced`, SearchFilter{}); err != nil {
		t.Errorf("operator input errored: %v", err)
	}
}

// TestReasoningSemanticsAcrossProviders pins the provider-specific
// reasoning contract end to end: OpenCode reports reasoning ADDITIVELY
// (folded into billable output by its adapter), Codex reports it as a
// SUBSET of output (never re-added). If either side regresses — dropped
// OpenCode reasoning or double-counted Codex reasoning — the exact
// token totals here break.
func TestReasoningSemanticsAcrossProviders(t *testing.T) {
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := func(dir string) []string {
		p, err := filepath.Abs(filepath.Join("../../testdata/agents", dir))
		if err != nil {
			t.Fatal(err)
		}
		return []string{p}
	}
	runner := ingest.New(store, table, codex.New(), opencode.New())
	if _, err := runner.Run(context.Background(), ingest.Options{
		ConfigRoots: map[canon.AgentSlug][]string{
			codex.Slug:    fixtures("codex"),
			opencode.Slug: fixtures("opencode"),
		},
		Getenv: func(string) string { return "" },
		Home:   "/nonexistent",
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	svc := New(store, table)

	sessions, err := svc.Sessions(context.Background(), SessionsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var oc, cx *SessionSummary
	for i := range sessions {
		switch sessions[i].Agent {
		case "opencode":
			oc = &sessions[i]
		case "codex":
			cx = &sessions[i]
		}
	}
	if oc == nil || cx == nil {
		t.Fatalf("missing sessions: opencode=%v codex=%v", oc != nil, cx != nil)
	}

	// OpenCode fixture: msg_002 output 510 + reasoning 90, msg_003
	// output 120 + reasoning 30 → billable output 750 (720 would mean
	// reasoning was dropped).
	if oc.Tokens.Output != 750 {
		t.Errorf("opencode output = %d, want 750 (reasoning folded in)", oc.Tokens.Output)
	}
	// msg_003 carries no reported cost, so its 450 tokens price through
	// the fallback — the total must exceed msg_002's reported 0.0142.
	if oc.CostUSD <= 0.0142 {
		t.Errorf("opencode cost = %v, want > 0.0142 (fallback priced the reasoning message)", oc.CostUSD)
	}

	// Codex fixture: cumulative output 1220 with reasoning 800 as a
	// SUBSET — 2020 would mean reasoning was double-counted.
	if cx.Tokens.Output != 1220 {
		t.Errorf("codex output = %d, want 1220 (reasoning is a subset, not added)", cx.Tokens.Output)
	}
}

// TestBlocksDistinctSessionsAcrossModels: three sessions share one
// 5-hour window with disjoint model sets (two on model-a, one on
// model-b). The window must report 3 sessions — the old per-model
// maximum reported 2, presenting a floor as an exact count.
func TestBlocksDistinctSessionsAcrossModels(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// All inside the same UTC-aligned window (10:00–15:00).
	at := func(s string) time.Time {
		ts, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatal(err)
		}
		return ts
	}
	seed := []struct {
		session, model, ts string
	}{
		{"blk-1", "model-a", "2026-07-01T10:05:00Z"},
		{"blk-2", "model-b", "2026-07-01T10:30:00Z"},
		{"blk-3", "model-a", "2026-07-01T11:00:00Z"},
	}
	for _, s := range seed {
		id, err := w.UpsertSession(canon.Session{
			Agent: "claude-code", ExternalID: s.session,
		}, "h-"+s.session)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.InsertMessage(id, "claude-code", canon.Message{
			Seq: 0, Role: canon.RoleAssistant, Kind: canon.KindMessage,
			CreatedAt: at(s.ts), Model: s.model,
			Usage: &canon.Usage{InputTokens: 100, OutputTokens: 10},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	blocks, err := New(store, table).Blocks(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 {
		t.Fatalf("blocks = %d, want 1 window", len(blocks))
	}
	if blocks[0].Sessions != 3 {
		t.Errorf("window sessions = %d, want 3 true distinct", blocks[0].Sessions)
	}
	if blocks[0].Messages != 3 {
		t.Errorf("window messages = %d, want 3", blocks[0].Messages)
	}
}
