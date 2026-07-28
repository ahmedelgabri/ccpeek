package db

import (
	"context"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// stubPricer prices exactly one model, so tests can tell the priced path
// from the unpriced one without depending on snapshot contents.
type stubPricer map[string]pricing.Rate

func (p stubPricer) Lookup(model string) (pricing.Rate, bool) {
	r, ok := p[model]
	return r, ok
}

// usageMessage writes one assistant message carrying usage into an
// existing session.
func usageMessage(t *testing.T, w *Writer, sessionID int64, agent canon.AgentSlug, seq int, model string, at time.Time, u canon.Usage) {
	t.Helper()
	if err := w.InsertMessage(sessionID, agent, canon.Message{
		Seq:       seq,
		Role:      canon.RoleAssistant,
		Model:     model,
		CreatedAt: at,
		Usage:     &u,
	}); err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
}

// A message may carry usage with no model at all — Codex attaches a
// token_count delta before turn_context names the model. Regenerating
// rollups used to index an empty candidate slice inside the pricer and
// panic on the ingest goroutine, taking the whole process down.
func TestRegenerateRollupsBlankModelIsUnpriced(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	w := beginWrite(t, s)
	sess := testSession("blank-model")
	sess.Agent = "codex"
	id, err := w.UpsertSession(sess, "hash")
	if err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	usageMessage(t, w, id, "codex", 0, "", time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		canon.Usage{InputTokens: 100, OutputTokens: 50})
	if err := w.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	table, err := pricing.Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if err := s.RegenerateRollups(ctx, table); err != nil {
		t.Fatalf("RegenerateRollups: %v", err)
	}

	var priced int
	var cost float64
	var in int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT priced, cost_usd, input_tokens FROM rollup_usage_daily`).
		Scan(&priced, &cost, &in); err != nil {
		t.Fatalf("reading rollup: %v", err)
	}
	if priced != 0 {
		t.Errorf("priced = %d, want 0 (an unknown model must be flagged, not silently $0)", priced)
	}
	if cost != 0 {
		t.Errorf("cost = %v, want 0", cost)
	}
	if in != 100 {
		t.Errorf("input_tokens = %d, want 100 — tokens must still be counted", in)
	}
}

// Sessions are not additive across rollup rows: one session spanning two
// models on one day contributes two rows, and summing them would report
// two sessions. The count comes from the session-day side table instead.
func TestUsageSessionsAreDistinctPerGroup(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	w := beginWrite(t, s)
	sess := testSession("multi-model")
	id, err := w.UpsertSession(sess, "hash")
	if err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	day := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	usageMessage(t, w, id, "claude-code", 0, "model-a", day, canon.Usage{InputTokens: 10})
	usageMessage(t, w, id, "claude-code", 1, "model-b", day, canon.Usage{InputTokens: 20})
	if err := w.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if err := s.RegenerateWorkspaces(ctx); err != nil {
		t.Fatalf("RegenerateWorkspaces: %v", err)
	}
	if err := s.RegenerateRollups(ctx, stubPricer{}); err != nil {
		t.Fatalf("RegenerateRollups: %v", err)
	}

	if n := count(t, s, `SELECT COUNT(*) FROM rollup_usage_daily`); n != 2 {
		t.Fatalf("rollup rows = %d, want 2 (one per model)", n)
	}
	// The side table carries the full rollup dimensions, so the session
	// appears once per model — but COUNT(DISTINCT session_id) over any
	// grouping of it is the true figure, which SUM over the aggregate
	// rows is not.
	if n := count(t, s, `SELECT COUNT(*) FROM rollup_session_days`); n != 2 {
		t.Fatalf("rollup_session_days rows = %d, want 2 (one per model)", n)
	}
	if n := count(t, s, `SELECT SUM(sessions) FROM rollup_usage_daily`); n != 2 {
		t.Fatalf("precondition: summing rollup sessions gives %d, expected the naive double count of 2", n)
	}
	if n := count(t, s, `SELECT COUNT(DISTINCT session_id) FROM rollup_session_days WHERE day = '2026-07-01'`); n != 1 {
		t.Errorf("distinct sessions on the day = %d, want 1", n)
	}
	if n := count(t, s, `SELECT COUNT(DISTINCT session_id) FROM rollup_session_days WHERE model = 'model-a'`); n != 1 {
		t.Errorf("distinct sessions on model-a = %d, want 1", n)
	}
}

// Rollups are the aggregate surface; the session-day side table must be
// rebuilt with them, never left behind from a previous pass.
func TestRegenerateRollupsReplacesSessionDays(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	w := beginWrite(t, s)
	id, err := w.UpsertSession(testSession("s1"), "hash")
	if err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}
	usageMessage(t, w, id, "claude-code", 0, "model-a",
		time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), canon.Usage{InputTokens: 10})
	if err := w.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if err := s.RegenerateRollups(ctx, stubPricer{}); err != nil {
		t.Fatalf("RegenerateRollups: %v", err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM rollup_session_days`); n != 1 {
		t.Fatalf("session-day rows = %d, want 1", n)
	}

	// Delete the usage and regenerate: both tables must empty out.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM message_usage`); err != nil {
		t.Fatalf("clearing usage: %v", err)
	}
	if err := s.RegenerateRollups(ctx, stubPricer{}); err != nil {
		t.Fatalf("RegenerateRollups: %v", err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM rollup_session_days`); n != 0 {
		t.Errorf("stale session-day rows = %d, want 0", n)
	}
}
