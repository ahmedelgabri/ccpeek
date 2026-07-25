package query

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

	// LIKE metacharacters are literal, matching how the commands and
	// history filters in this package already behave. Unescaped, "%" is a
	// wildcard that matches every session — the filter would appear to do
	// nothing rather than to find nothing.
	for _, q := range []string{"%", "_ate limiting", "rate%limiting"} {
		got, err := s.Sessions(ctx, SessionsFilter{Query: q})
		if err != nil {
			t.Fatalf("Sessions(%q): %v", q, err)
		}
		if len(got) != 0 {
			t.Errorf("title filter %q matched %d sessions, want 0 — wildcards must be literal", q, len(got))
		}
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

	// until is INCLUSIVE on every transport, so a single-day range names
	// the same date twice.
	byDay, err := s.Usage(ctx, UsageFilter{GroupBy: "day", Since: "2026-07-01", Until: "2026-07-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byDay) != 1 || byDay[0].Group != "2026-07-01" {
		t.Errorf("day groups = %+v, want exactly 2026-07-01", byDay)
	}
	twoDays, err := s.Usage(ctx, UsageFilter{GroupBy: "day", Since: "2026-07-01", Until: "2026-07-02"})
	if err != nil {
		t.Fatal(err)
	}
	if len(twoDays) != 2 {
		t.Errorf("two-day range = %d groups, want 2 (the bound is inclusive)", len(twoDays))
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

	if err := store.RegenerateRollups(ctx, table); err != nil {
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

// TestNoSilentCaps seeds one row past every previously clamped surface
// and proves completeness: Artifacts honors an explicit 501 limit and
// pages to the full set, Usage returns all groups by default (101 days
// would have been cut to 100), and SessionTools pages to the full set
// with a default of everything.
func TestNoSilentCaps(t *testing.T) {
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
	sessionID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "caps-session",
	}, "h-caps")
	if err != nil {
		t.Fatal(err)
	}
	// 101 usage days (the old Usage default returned 100).
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 101; i++ {
		if err := w.InsertMessage(sessionID, "claude-code", canon.Message{
			Seq: i, Role: canon.RoleAssistant, Kind: canon.KindMessage,
			CreatedAt: base.AddDate(0, 0, i), Model: "model-a",
			Usage: &canon.Usage{InputTokens: 10, OutputTokens: 1},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// 501 artifacts (the old Artifacts clamp was 500).
	for i := 0; i < 501; i++ {
		if _, err := w.UpsertArtifact(canon.Artifact{
			Agent: "claude-code", Kind: canon.ArtifactPlan,
			Name: fmt.Sprintf("plan-%03d.md", i), Content: "x",
		}, fmt.Sprintf("h-%03d", i)); err != nil {
			t.Fatal(err)
		}
	}
	// 5 tool calls, paged by 2.
	for i := 0; i < 5; i++ {
		if err := w.InsertToolCall(sessionID, canon.ToolCall{
			Seq: i, Name: "Bash", Kind: canon.ToolShell,
			Input: json.RawMessage(fmt.Sprintf(`{"command":"echo %d"}`, i)),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := store.RegenerateRollups(ctx, table); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	// Usage: all 101 day groups with no limit given.
	usage, err := svc.Usage(ctx, UsageFilter{GroupBy: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 101 {
		t.Errorf("usage day groups = %d, want 101 (silently capped?)", len(usage))
	}

	// Artifacts: explicit limit past the old clamp is honored…
	arts, err := svc.Artifacts(ctx, ArtifactsFilter{Limit: 501})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 501 {
		t.Errorf("artifacts with limit 501 = %d, want 501", len(arts))
	}
	// …and offset pages cover the full set exactly once.
	seen := map[string]bool{}
	for offset := 0; ; offset += 100 {
		page, err := svc.Artifacts(ctx, ArtifactsFilter{Limit: 100, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range page {
			if seen[a.Name] {
				t.Errorf("artifact %q returned twice across pages", a.Name)
			}
			seen[a.Name] = true
		}
		if len(page) < 100 {
			break
		}
	}
	if len(seen) != 501 {
		t.Errorf("artifacts across pages = %d, want 501", len(seen))
	}

	// Tools: default returns everything; pages of 2 cover the set.
	all, err := svc.SessionTools(ctx, "claude-code", "caps-session", ToolsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Errorf("tools default = %d, want 5 (all)", len(all))
	}
	var paged []ToolCallRow
	for offset := 0; ; offset += 2 {
		page, err := svc.SessionTools(ctx, "claude-code", "caps-session",
			ToolsFilter{Limit: 2, Offset: offset})
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, page...)
		if len(page) < 2 {
			break
		}
	}
	if len(paged) != 5 {
		t.Errorf("tools across pages = %d, want 5", len(paged))
	}
}

// TestToolChipsAndLazyDetail pins the split projection: compact chip
// rows are range-scoped and capped (no timestamps, no diff payloads —
// those can reach 16 KiB each), the plain list carries full detail but
// still no excerpts, and the per-call detail lookup is where old/new
// live, fetched only when a row expands.
func TestToolChipsAndLazyDetail(t *testing.T) {
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
	sessionID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "chips-session",
	}, "h-chips")
	if err != nil {
		t.Fatal(err)
	}
	longCmd := strings.Repeat("echo very-long-command && ", 20)
	bigOld := strings.Repeat("o", 4000)
	bigNew := strings.Repeat("n", 4000)
	edit, _ := json.Marshal(map[string]string{
		"file_path": "a.go", "old_string": bigOld, "new_string": bigNew,
	})
	calls := []canon.ToolCall{
		{
			Seq: 0, MessageSeq: 1, Name: "Bash", Kind: canon.ToolShell,
			Input: json.RawMessage(`{"command":"` + longCmd + `"}`),
		},
		{
			Seq: 1, MessageSeq: 5, Name: "Edit", Kind: canon.ToolFileEdit,
			Input: edit, FilePath: "a.go",
		},
		{
			Seq: 2, MessageSeq: 9, Name: "Read", Kind: canon.ToolFileRead,
			Input: json.RawMessage(`{"file_path":"b.go"}`), FilePath: "b.go",
		},
	}
	for _, c := range calls {
		if err := w.InsertToolCall(sessionID, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	// Compact chips scoped to message seqs 2..9: the shell call at 1 is
	// outside the range; detail is capped at chip size; At is empty.
	chips, err := svc.SessionTools(ctx, "claude-code", "chips-session",
		ToolsFilter{Compact: true, FromSeq: 2, ToSeq: 9})
	if err != nil {
		t.Fatal(err)
	}
	if len(chips) != 2 || chips[0].Seq != 1 || chips[1].Seq != 2 {
		t.Fatalf("chips = %+v, want calls 1 and 2", chips)
	}
	for _, c := range chips {
		if len(c.Detail) > 120 {
			t.Errorf("chip detail = %d bytes, want <= 120", len(c.Detail))
		}
		if c.At != "" {
			t.Errorf("chip carries a timestamp: %q", c.At)
		}
	}

	// The full list keeps full detail (the long shell command) but the
	// row type carries no excerpt fields at all — those live behind the
	// per-call detail.
	full, err := svc.SessionTools(ctx, "claude-code", "chips-session", ToolsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 3 || len(full[0].Detail) <= 120 {
		t.Fatalf("full list = %d rows, first detail %d bytes", len(full), len(full[0].Detail))
	}

	detail, err := svc.SessionToolDetail(ctx, "claude-code", "chips-session", 1)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Old != bigOld || detail.New != bigNew {
		t.Errorf("detail excerpts = %d/%d bytes, want %d/%d",
			len(detail.Old), len(detail.New), len(bigOld), len(bigNew))
	}
	if _, err := svc.SessionToolDetail(ctx, "claude-code", "chips-session", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown seq error = %v, want ErrNotFound", err)
	}
}

// TestHistoryQueryable: prompt history is a first-class read — filter
// by agent and prompt substring, page, and see the session link when
// one resolved. (It was previously stored but unreachable.)
func TestHistoryQueryable(t *testing.T) {
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
	if _, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "hist-sess",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	entries := []canon.HistoryEntry{
		{
			Agent: "claude-code", Display: "fix the rate limiter",
			Timestamp: time.UnixMilli(1751713260000), SessionExternalID: "hist-sess",
		},
		{
			Agent: "claude-code", Display: "write release notes",
			Timestamp: time.UnixMilli(1751713270000),
		},
	}
	for _, e := range entries {
		if err := w.InsertHistory(e, "/x/history.jsonl"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	all, err := svc.History(ctx, HistoryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("history = %d rows, want 2", len(all))
	}
	if all[0].Display != "write release notes" {
		t.Errorf("order = %q first, want newest first", all[0].Display)
	}
	if all[1].SessionID != "hist-sess" {
		t.Errorf("linked entry sessionId = %q, want hist-sess", all[1].SessionID)
	}
	if all[0].At == "" {
		t.Error("timestamp not rendered")
	}

	filtered, err := svc.History(ctx, HistoryFilter{Query: "rate limiter"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Display != "fix the rate limiter" {
		t.Errorf("substring filter = %+v", filtered)
	}
}

// usageSeed is one seeded usage-bearing message.
type usageSeed struct{ session, model, ts string }

// seedUsage writes one usage-bearing assistant message per entry.
func seedUsage(t *testing.T, store *db.Store, rows []usageSeed) {
	t.Helper()
	ctx := context.Background()
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seq := map[string]int{}
	for _, r := range rows {
		ts, err := time.Parse(time.RFC3339, r.ts)
		if err != nil {
			t.Fatal(err)
		}
		// Session timestamps track the messages, so date filters over
		// sessions and over usage see the same corpus.
		id, err := w.UpsertSession(canon.Session{
			Agent: "claude-code", ExternalID: r.session, CWD: "/home/u/proj",
			CreatedAt: ts, ModifiedAt: ts,
		}, "h-"+r.session)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.InsertMessage(id, "claude-code", canon.Message{
			Seq: seq[r.session], Role: canon.RoleAssistant, Kind: canon.KindMessage,
			CreatedAt: ts, Model: r.model,
			Usage: &canon.Usage{InputTokens: 100, OutputTokens: 10},
		}); err != nil {
			t.Fatal(err)
		}
		seq[r.session]++
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
}

func newStore(t *testing.T) (*db.Store, *pricing.Table) {
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
	return store, table
}

// Session counts must stay true distinct counts per group after the
// switch to the rollup side table — a session spanning two models on one
// day is ONE session that day, not two, and one spanning two days is one
// session per day but one session overall when grouped by model.
func TestUsageDistinctSessionsPerGrouping(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	seedUsage(t, store, []usageSeed{
		{"s1", "model-a", "2026-07-01T10:00:00Z"},
		{"s1", "model-b", "2026-07-01T11:00:00Z"}, // same session+day, 2nd model
		{"s1", "model-a", "2026-07-02T10:00:00Z"}, // same session, next day
		{"s2", "model-a", "2026-07-01T12:00:00Z"},
	})
	if err := store.RegenerateWorkspaces(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RegenerateRollups(ctx, table); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	byDay, err := svc.Usage(ctx, UsageFilter{GroupBy: "day"})
	if err != nil {
		t.Fatal(err)
	}
	days := map[string]int64{}
	for _, r := range byDay {
		days[r.Group] = r.Sessions
	}
	if days["2026-07-01"] != 2 {
		t.Errorf("2026-07-01 sessions = %d, want 2 (s1 counted once despite two models)", days["2026-07-01"])
	}
	if days["2026-07-02"] != 1 {
		t.Errorf("2026-07-02 sessions = %d, want 1", days["2026-07-02"])
	}

	byModel, err := svc.Usage(ctx, UsageFilter{GroupBy: "model"})
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]int64{}
	for _, r := range byModel {
		models[r.Group] = r.Sessions
	}
	if models["model-a"] != 2 {
		t.Errorf("model-a sessions = %d, want 2 (s1 counted once across two days)", models["model-a"])
	}
	if models["model-b"] != 1 {
		t.Errorf("model-b sessions = %d, want 1", models["model-b"])
	}

	byProject, err := svc.Usage(ctx, UsageFilter{GroupBy: "project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byProject) != 1 || byProject[0].Sessions != 2 {
		t.Errorf("project groups = %+v, want one group with 2 sessions", byProject)
	}
}

// The date filters still narrow the distinct counts the same way they
// narrow the aggregates.
func TestUsageDistinctSessionsRespectFilters(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	seedUsage(t, store, []usageSeed{
		{"s1", "model-a", "2026-07-01T10:00:00Z"},
		{"s2", "model-a", "2026-07-05T10:00:00Z"},
		{"s3", "model-b", "2026-07-09T10:00:00Z"},
	})
	if err := store.RegenerateRollups(ctx, table); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	rows, err := svc.Usage(ctx, UsageFilter{
		GroupBy: "agent", Since: "2026-07-04", Until: "2026-07-08",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one agent group", rows)
	}
	if rows[0].Sessions != 1 {
		t.Errorf("sessions in range = %d, want 1 (only s2)", rows[0].Sessions)
	}

	// The inclusive upper bound reaches the named day itself.
	rows, err = svc.Usage(ctx, UsageFilter{
		GroupBy: "agent", Since: "2026-07-04", Until: "2026-07-09",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Sessions != 2 {
		t.Errorf("range ending on the 9th = %+v, want 2 sessions (s2 and s3)", rows)
	}

	rows, err = svc.Usage(ctx, UsageFilter{GroupBy: "agent", Model: "model-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Sessions != 1 {
		t.Errorf("model-filtered rows = %+v, want one group with 1 session", rows)
	}
}

// Blocks anchors its window on the newest indexed usage, not on wall
// clock: an archive whose last session was weeks ago must still show that
// week's windows rather than an empty view.
func TestBlocksAnchorsOnNewestActivity(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	// Deliberately in the past relative to any plausible "now".
	seedUsage(t, store, []usageSeed{
		{"old-1", "model-a", "2020-03-01T10:05:00Z"},
		{"old-2", "model-a", "2020-03-01T16:00:00Z"}, // the next window
	})
	// Blocks anchors on the rollups, which every ingest pass regenerates.
	if err := store.RegenerateRollups(ctx, table); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	blocks, err := svc.Blocks(ctx, "", 24)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2 windows from a stale archive", len(blocks))
	}
	if blocks[0].Start < blocks[1].Start {
		t.Error("blocks are not newest-first")
	}
	for _, b := range blocks {
		if b.Active {
			t.Errorf("window %s marked active, but now is not inside it", b.Start)
		}
	}
}

// The limit bounds how far back the aggregate reaches, so old history is
// not scanned to answer about recent windows.
func TestBlocksLimitBoundsTheWindowRange(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	// Five windows, one every 5 hours, plus one far older.
	seedUsage(t, store, []usageSeed{
		{"a", "model-a", "2020-01-01T00:00:00Z"}, // ancient, far outside any limit
		{"b", "model-a", "2026-07-01T00:05:00Z"},
		{"c", "model-a", "2026-07-01T05:05:00Z"},
		{"d", "model-a", "2026-07-01T10:05:00Z"},
		{"e", "model-a", "2026-07-01T15:05:00Z"},
	})
	if err := store.RegenerateRollups(ctx, table); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	all, err := svc.Blocks(ctx, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	// The ancient row is ~2400 windows back, well past a 200-window reach.
	if len(all) != 4 {
		t.Fatalf("blocks = %d, want the 4 recent windows", len(all))
	}

	few, err := svc.Blocks(ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(few) > 3 {
		t.Errorf("blocks with limit 2 = %d, want at most 3 (limit plus slack)", len(few))
	}
	if few[0].Start != all[0].Start {
		t.Errorf("newest window = %s, want %s", few[0].Start, all[0].Start)
	}
}

// An empty store answers with no windows rather than failing.
func TestBlocksEmptyStore(t *testing.T) {
	store, table := newStore(t)
	blocks, err := New(store, table).Blocks(context.Background(), "", 24)
	if err != nil {
		t.Fatalf("Blocks on an empty store: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("blocks = %d, want 0", len(blocks))
	}
}

// Snippet delimiters must be distinguishable from the content. With
// brackets they were not: the corpus is source code and transcripts, so a
// hit inside a markdown link, a slice expression or a JSON array produced
// stray, unbalanced marks in the UI.
func TestSearchSnippetDelimitersSurviveBracketyContent(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "brackets",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	// Short enough that the whole line fits inside the snippet window, so
	// a missing bracket means mangling rather than elision.
	body := "[docs] wins[:limit] [1,2,3] zebrafish"
	if err := w.InsertMessage(id, "claude-code", canon.Message{
		Seq: 0, Role: canon.RoleUser, Content: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertSearchDoc(id, 0, "message", 0, "", body); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	hits, err := New(store, table).Search(ctx, "zebrafish", SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(hits))
	}
	snippet := hits[0].Snippet

	// The match is wrapped in the control-character delimiters…
	if !strings.Contains(snippet, SnippetOpen+"zebrafish"+SnippetClose) {
		t.Errorf("match not delimited in %q", snippet)
	}
	// …and exactly one pair of them exists, however many brackets the
	// content carries.
	if got := strings.Count(snippet, SnippetOpen); got != 1 {
		t.Errorf("open delimiters = %d, want 1, in %q", got, snippet)
	}
	if got := strings.Count(snippet, SnippetClose); got != 1 {
		t.Errorf("close delimiters = %d, want 1, in %q", got, snippet)
	}
	// The literal brackets are still literal brackets.
	for _, literal := range []string{"[docs]", "wins[:limit]", "[1,2,3]"} {
		if !strings.Contains(snippet, literal) {
			t.Errorf("content %q was mangled out of %q", literal, snippet)
		}
	}
}

// "until" means the same thing on every transport: inclusive. It used to
// be exclusive in the service, with only the web UI compensating by
// adding a day client-side, so the same nominal argument produced
// different ranges depending on who sent it.
func TestUntilIsInclusiveEverywhere(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	seedUsage(t, store, []usageSeed{
		{"before", "model-a", "2026-07-01T10:00:00Z"},
		{"onTheDay", "model-a", "2026-07-02T23:59:00Z"},
		{"after", "model-a", "2026-07-03T00:01:00Z"},
	})
	if err := store.RegenerateWorkspaces(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.RegenerateRollups(ctx, table); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	// Usage: the named day is in.
	days, err := svc.Usage(ctx, UsageFilter{GroupBy: "day", Until: "2026-07-02"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range days {
		got[r.Group] = true
	}
	if !got["2026-07-02"] {
		t.Error("usage until=2026-07-02 excluded that day")
	}
	if got["2026-07-03"] {
		t.Error("usage until=2026-07-02 reached past the bound")
	}

	// Sessions: same rule, against modified_at.
	sessions, err := svc.Sessions(ctx, SessionsFilter{Until: "2026-07-02"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range sessions {
		ids[s.ID] = true
	}
	if !ids["onTheDay"] {
		t.Error("sessions until=2026-07-02 excluded a session modified that day")
	}
	if ids["after"] {
		t.Error("sessions until=2026-07-02 reached past the bound")
	}

	// A single-day window is since == until and is not empty.
	oneDay, err := svc.Sessions(ctx, SessionsFilter{Since: "2026-07-02", Until: "2026-07-02"})
	if err != nil {
		t.Fatal(err)
	}
	if len(oneDay) != 1 || oneDay[0].ID != "onTheDay" {
		t.Errorf("single-day window = %+v, want just onTheDay", oneDay)
	}
}

// Artifact size is reported in BYTES: SQLite's LENGTH() counts characters
// on a TEXT value, so for non-ASCII content the figure the UI formats as
// KB/MB was under the real one.
func TestArtifactSizeIsBytes(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Repeat("日", 100) // 100 characters, 300 bytes
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "multibyte.md",
		Content: content,
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	svc := New(store, table)
	list, err := svc.Artifacts(ctx, ArtifactsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("artifacts = %d, want 1", len(list))
	}
	if list[0].Size != len(content) {
		t.Errorf("list size = %d, want %d bytes (not %d characters)",
			list[0].Size, len(content), len([]rune(content)))
	}

	detail, err := svc.Artifact(ctx, "claude-code", "plan", "multibyte.md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Size != len(content) {
		t.Errorf("detail size = %d, want %d bytes", detail.Size, len(content))
	}
}

// workspaces.canonical_path holds a canonical path, so a caller-supplied
// one has to be canonicalized before it is compared — otherwise the
// write-side canonicalization silently made `?project=/home/u/proj/`
// match nothing while `/home/u/proj` matched everything.
func TestProjectFilterCanonicalizesTheCallerPath(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	seedUsage(t, store, []usageSeed{
		{"in-proj", "model-a", "2026-07-01T10:00:00Z"},
	})
	if err := store.RegenerateWorkspaces(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	// Every spelling of the same directory finds the session.
	for _, project := range []string{
		"/home/u/proj",
		"/home/u/proj/",
		"/home/u/proj//",
		"/home/u/./proj",
		"  /home/u/proj  ",
	} {
		got, err := svc.Sessions(ctx, SessionsFilter{Project: project})
		if err != nil {
			t.Fatalf("project=%q: %v", project, err)
		}
		if len(got) != 1 || got[0].ID != "in-proj" {
			t.Errorf("project=%q matched %d sessions, want 1", project, len(got))
		}
	}

	// A genuinely different directory still matches nothing.
	if got, err := svc.Sessions(ctx, SessionsFilter{Project: "/home/u/other"}); err != nil || len(got) != 0 {
		t.Errorf("unrelated project matched %d sessions (err %v), want 0", len(got), err)
	}
}
