package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/pi"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

func fixturePath(t *testing.T, agentDir string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("../../testdata/agents", agentDir))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newRunner(t *testing.T) (*Runner, *db.Store) {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatalf("pricing: %v", err)
	}
	return New(store, table, claude.New(), pi.New()), store
}

func fixtureOptions(t *testing.T) Options {
	return Options{
		ConfigRoots: map[canon.AgentSlug][]string{
			claude.Slug: {fixturePath(t, "claude-code")},
			pi.Slug:     {fixturePath(t, "pi")},
		},
		Getenv: func(string) string { return "" },
		Home:   "/nonexistent-home",
	}
}

func queryInt(t *testing.T, store *db.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func TestRunOverFixtureCorpus(t *testing.T) {
	runner, store := newRunner(t)
	report, err := runner.Run(context.Background(), fixtureOptions(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 13 claude sources (3 sessions + 10 sidecars) + 2 pi sessions.
	if report.FilesSeen != 15 || report.FilesChanged != 15 {
		t.Errorf("files seen/changed = %d/%d, want 15/15", report.FilesSeen, report.FilesChanged)
	}
	if report.Sessions != 5 {
		t.Errorf("sessions = %d, want 5 (3 claude + 2 pi)", report.Sessions)
	}
	if report.Artifacts != 9 {
		t.Errorf("artifacts = %d, want 9", report.Artifacts)
	}
	if report.History != 2 {
		t.Errorf("history = %d, want 2", report.History)
	}
	// Artifact links resolved against ingested sessions — including the
	// plan (matched to session 2's ExitPlanMode call by text) and the
	// memory (matched to session 2's Write call by path).
	if n := queryInt(t, store, `SELECT COUNT(*) FROM artifact_sessions`); n != 6 {
		t.Errorf("artifact_sessions = %d, want 6 (todo, task, file-history, facet, plan, memory)", n)
	}
	for _, kind := range []string{"plan", "memory"} {
		if n := queryInt(t, store, `
			SELECT COUNT(*) FROM artifact_sessions ass
			JOIN artifacts a ON a.id = ass.artifact_id
			JOIN sessions s ON s.id = ass.session_id
			WHERE a.kind = ? AND ass.evidence = 'content_ref'
			  AND s.external_id = '22222222-aaaa-bbbb-cccc-222222222222'`, kind); n != 1 {
			t.Errorf("%s content_ref links = %d, want 1", kind, n)
		}
	}
	// Two corrupted fixture lines (one per agent corpus).
	if len(report.Issues) != 2 {
		t.Errorf("issues = %v, want 2 parse warnings", report.Issues)
	}
	if report.Status != "ok" {
		t.Errorf("status = %q (warnings must not degrade status)", report.Status)
	}

	// Usage dedupe across resumed Claude sessions: msg_alpha_3/req_alpha_3
	// appears in two files but counts once. Claude fixture usage rows:
	// session1 has 3, session2 has 1 new (dupe skipped), session3 has 3.
	// Pi: 3 + 1.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM message_usage`); n != 11 {
		t.Errorf("message_usage rows = %d, want 11 (dedupe collapsed the resume)", n)
	}

	// Pi fork relation resolved within the run.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM session_relations WHERE kind = 'fork_of'`); n != 1 {
		t.Errorf("fork_of relations = %d, want 1", n)
	}
	if report.LinksPending != 0 {
		t.Errorf("pending links = %d, want 0", report.LinksPending)
	}

	// Workspace facet regenerated from session cwd.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM workspaces`); n != 1 {
		t.Errorf("workspaces = %d, want 1", n)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM session_workspaces`); n != 5 {
		t.Errorf("session_workspaces = %d, want 5", n)
	}

	// Search index populated and queryable.
	if n := queryInt(t, store,
		`SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'limiting'`); n == 0 {
		t.Error("search index has no hits for fixture content")
	}

	// Run telemetry recorded.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM ingest_runs WHERE status = 'ok'`); n != 1 {
		t.Errorf("ok ingest_runs = %d, want 1", n)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM ingest_issues`); n != 2 {
		t.Errorf("ingest_issues = %d, want 2", n)
	}

	// Rollups regenerated with cost: priced rows for known models, an
	// unpriced row for the sidechain's experimental model.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM rollup_usage_daily`); n == 0 {
		t.Fatal("no rollup rows")
	}
	if n := queryInt(t, store,
		`SELECT COUNT(*) FROM rollup_usage_daily WHERE priced = 0 AND model = 'experimental-audit-model'`); n != 1 {
		t.Errorf("unpriced rollup rows for unknown model = %d, want 1", n)
	}
	var cost float64
	if err := store.DB().QueryRowContext(context.Background(),
		`SELECT SUM(cost_usd) FROM rollup_usage_daily`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	// Pi's reported costs alone sum to ~0.019; computed Claude costs add
	// more. Exact value depends on the pricing snapshot — assert sanity.
	if cost <= 0.019 || cost > 1.0 {
		t.Errorf("total rollup cost = %v, want (0.019, 1.0]", cost)
	}
}

func TestSecondRunIsNoop(t *testing.T) {
	runner, _ := newRunner(t)
	if _, err := runner.Run(context.Background(), fixtureOptions(t)); err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), fixtureOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesChanged != 0 || report.Sessions != 0 {
		t.Errorf("second run changed=%d sessions=%d, want 0/0", report.FilesChanged, report.Sessions)
	}
}

func TestChangedFileReingestsWithoutDuplication(t *testing.T) {
	runner, store := newRunner(t)

	// Copy the corpus so we can mutate a file.
	tmp := t.TempDir()
	copyDir(t, fixturePath(t, "claude-code"), filepath.Join(tmp, "claude-code"))
	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{filepath.Join(tmp, "claude-code")}

	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	before := queryInt(t, store, `SELECT COUNT(*) FROM messages`)

	// Append one entry to a session file.
	target := filepath.Join(tmp, "claude-code", "projects", "-home-u-demo-api",
		"22222222-aaaa-bbbb-cccc-222222222222.jsonl")
	line := `{"parentUuid":"u-103","isSidechain":false,"cwd":"/home/u/demo/api","sessionId":"22222222-aaaa-bbbb-cccc-222222222222","gitBranch":"feat/limits","type":"user","message":{"role":"user","content":"and lint it"},"uuid":"u-104","timestamp":"2026-07-02T09:10:00.000Z"}` + "\n"
	f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(line); err != nil {
		t.Fatal(err)
	}
	f.Close()

	report, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesChanged != 1 {
		t.Fatalf("files changed = %d, want 1", report.FilesChanged)
	}
	after := queryInt(t, store, `SELECT COUNT(*) FROM messages`)
	if after != before+1 {
		t.Errorf("messages %d → %d, want +1 (session children replaced, not duplicated)", before, after)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 5 {
		t.Errorf("sessions = %d, want 5 (upsert, not duplicate)", n)
	}
}

// TestTouchedFileWithSameContentSkipsReingest covers the middle tier of
// change detection: a stat change (rewritten mtime) with identical bytes
// must not re-parse — and must refresh the stat signature so the next run
// takes the cheap path again.
func TestTouchedFileWithSameContentSkipsReingest(t *testing.T) {
	runner, store := newRunner(t)

	tmp := t.TempDir()
	copyDir(t, fixturePath(t, "claude-code"), filepath.Join(tmp, "claude-code"))
	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{filepath.Join(tmp, "claude-code")}

	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(tmp, "claude-code", "projects", "-home-u-demo-api",
		"22222222-aaaa-bbbb-cccc-222222222222.jsonl")
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatal(err)
	}

	report, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesChanged != 0 {
		t.Fatalf("files changed = %d after touch-only, want 0", report.FilesChanged)
	}

	// The stat signature was refreshed: a third run skips on stat alone.
	sigs, err := store.SourceSigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("f:%d:%d", fi.Size(), fi.ModTime().UnixNano())
	if got := sigs[target].StatSig; got != want {
		t.Errorf("stat sig after touch = %q, want %q", got, want)
	}
}

func TestMissingConfiguredRootSurfaces(t *testing.T) {
	runner, _ := newRunner(t)
	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{"/definitely/not/a/root"}

	report, err := runner.Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, is := range report.Issues {
		if is.Category == "root" && is.Agent == claude.Slug {
			found = true
		}
	}
	if !found {
		t.Errorf("missing explicit root produced no diagnostic: %v", report.Issues)
	}
}

func TestRebuildPreservesUserAnnotations(t *testing.T) {
	runner, store := newRunner(t)
	ctx := context.Background()
	if _, err := runner.Run(ctx, fixtureOptions(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, `
		INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		VALUES ('session', 'claude-code/11111111-aaaa-bbbb-cccc-111111111111', 'pin', '{}', '2026-07-10T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	opts := fixtureOptions(t)
	opts.Rebuild = true
	report, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.Sessions != 5 {
		t.Errorf("rebuild sessions = %d, want 5", report.Sessions)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM user_annotations`); n != 1 {
		t.Errorf("user_annotations = %d after rebuild, want 1", n)
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestAppendParsesOnlyNewBytes proves the cursor path end-to-end: an
// append re-parses nothing before the cursor, keeps the session's
// creation-time attributes, applies a cross-boundary tool result to the
// stored call, and advances the stored cursor.
func TestAppendParsesOnlyNewBytes(t *testing.T) {
	runner, store := newRunner(t)
	ctx := context.Background()

	tmp := t.TempDir()
	copyDir(t, fixturePath(t, "claude-code"), filepath.Join(tmp, "claude-code"))
	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{filepath.Join(tmp, "claude-code")}

	if _, err := runner.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(tmp, "claude-code", "projects", "-home-u-demo-api",
		"11111111-aaaa-bbbb-cccc-111111111111.jsonl")
	var title, createdAt string
	row := store.DB().QueryRowContext(ctx, `
		SELECT title, created_at FROM sessions WHERE external_id = '11111111-aaaa-bbbb-cccc-111111111111'`)
	if err := row.Scan(&title, &createdAt); err != nil {
		t.Fatal(err)
	}
	sigs, err := store.SourceSigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cursorBefore := sigs[target].ParseState
	if cursorBefore == "" {
		t.Fatal("no cursor recorded for the session source after a full parse")
	}
	msgIDsBefore := queryInt(t, store, `
		SELECT COALESCE(SUM(m.id), 0) FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE s.external_id = '11111111-aaaa-bbbb-cccc-111111111111'`)

	// Append a new tool call and, crucially, a result for a call that was
	// already indexed by the full parse (external id tu-002 from the
	// fixture) — it must update the stored row across the parse boundary.
	appendLines := `{"parentUuid":"u-105","isSidechain":false,"cwd":"/home/u/demo/api","sessionId":"11111111-aaaa-bbbb-cccc-111111111111","gitBranch":"main","type":"assistant","message":{"id":"msg_z","role":"assistant","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"toolu_late","name":"Bash","input":{"command":"go test"}}]},"uuid":"a-late","timestamp":"2026-07-01T14:00:00.000Z"}` + "\n" +
		`{"parentUuid":"a-late","isSidechain":false,"cwd":"/home/u/demo/api","sessionId":"11111111-aaaa-bbbb-cccc-111111111111","gitBranch":"main","type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tu-002","content":"refreshed output"}]},"uuid":"u-late","timestamp":"2026-07-01T14:00:05.000Z"}` + "\n"
	f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(appendLines); err != nil {
		t.Fatal(err)
	}
	f.Close()

	report, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesChanged != 1 || report.Messages != 2 {
		t.Fatalf("append run changed=%d messages=%d, want 1/2 (only the appended entries parsed)",
			report.FilesChanged, report.Messages)
	}

	// Pre-existing message rows are untouched, not cleared and re-inserted.
	msgIDsAfter := queryInt(t, store, `
		SELECT COALESCE(SUM(m.id), 0) FROM messages m
		JOIN sessions s ON s.id = m.session_id
		WHERE s.external_id = '11111111-aaaa-bbbb-cccc-111111111111'
		  AND m.external_id NOT IN ('a-late', 'u-late')`)
	if msgIDsAfter != msgIDsBefore {
		t.Errorf("pre-existing message row ids changed (%d → %d): session was re-parsed, not appended",
			msgIDsBefore, msgIDsAfter)
	}

	// Creation-time attributes survive; modification advances.
	var title2, createdAt2, modifiedAt2 string
	row = store.DB().QueryRowContext(ctx, `
		SELECT title, created_at, modified_at FROM sessions WHERE external_id = '11111111-aaaa-bbbb-cccc-111111111111'`)
	if err := row.Scan(&title2, &createdAt2, &modifiedAt2); err != nil {
		t.Fatal(err)
	}
	if title2 != title || createdAt2 != createdAt {
		t.Errorf("append rewrote creation attributes: title %q→%q, created %q→%q",
			title, title2, createdAt, createdAt2)
	}
	if modifiedAt2 != "2026-07-01T14:00:05Z" {
		t.Errorf("modified_at = %q, want the appended entry's timestamp", modifiedAt2)
	}

	// The new call landed with its external id and continued seq.
	if n := queryInt(t, store, `
		SELECT COUNT(*) FROM tool_calls WHERE external_id = 'toolu_late' AND seq = 2`); n != 1 {
		t.Errorf("appended tool call rows = %d, want 1 at seq 2", n)
	}

	// The cross-boundary result reached the stored call.
	var status, excerpt string
	row = store.DB().QueryRowContext(ctx,
		`SELECT result_status, result_excerpt FROM tool_calls WHERE external_id = 'tu-002'`)
	if err := row.Scan(&status, &excerpt); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || excerpt != "refreshed output" {
		t.Errorf("cross-boundary result = %q %q, want ok/refreshed output", status, excerpt)
	}

	// The cursor advanced.
	sigs, err = store.SourceSigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sigs[target].ParseState == cursorBefore {
		t.Error("cursor did not advance after the append")
	}
}

// TestRewrittenPrefixFallsBackToFullParse: when the already-parsed bytes
// change (history rewritten, not appended), the cursor is invalid and the
// source re-parses fully — without duplicating rows.
func TestRewrittenPrefixFallsBackToFullParse(t *testing.T) {
	runner, store := newRunner(t)
	ctx := context.Background()

	tmp := t.TempDir()
	copyDir(t, fixturePath(t, "claude-code"), filepath.Join(tmp, "claude-code"))
	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{filepath.Join(tmp, "claude-code")}

	if _, err := runner.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	before := queryInt(t, store, `SELECT COUNT(*) FROM messages`)

	// Rewrite the file's first line (change the recorded branch).
	target := filepath.Join(tmp, "claude-code", "projects", "-home-u-demo-api",
		"22222222-aaaa-bbbb-cccc-222222222222.jsonl")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := []byte(strings.Replace(string(data), `"gitBranch":"feat/limits"`, `"gitBranch":"feat/limitz"`, 1))
	if len(rewritten) != len(data) || string(rewritten) == string(data) {
		t.Fatal("fixture rewrite did not apply as a same-length prefix change")
	}
	if err := os.WriteFile(target, rewritten, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesChanged != 1 {
		t.Fatalf("files changed = %d, want 1", report.FilesChanged)
	}
	if report.Status != "ok" {
		t.Fatalf("status = %q, issues: %v", report.Status, report.Issues)
	}
	after := queryInt(t, store, `SELECT COUNT(*) FROM messages`)
	if after != before {
		t.Errorf("messages %d → %d after prefix rewrite, want unchanged (full re-parse, no dupes)", before, after)
	}
}

// TestHistoryAppendDoesNotDuplicate: history.jsonl re-parses whole on
// change; rows must be replaced per source, not appended again.
func TestHistoryAppendDoesNotDuplicate(t *testing.T) {
	runner, store := newRunner(t)
	ctx := context.Background()

	tmp := t.TempDir()
	copyDir(t, fixturePath(t, "claude-code"), filepath.Join(tmp, "claude-code"))
	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{filepath.Join(tmp, "claude-code")}

	if _, err := runner.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	before := queryInt(t, store, `SELECT COUNT(*) FROM history`)

	target := filepath.Join(tmp, "claude-code", "history.jsonl")
	f, err := os.OpenFile(target, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"display":"one more prompt","timestamp":1751443200000}` + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := runner.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}
	after := queryInt(t, store, `SELECT COUNT(*) FROM history`)
	if after != before+1 {
		t.Errorf("history rows %d → %d after one append, want +1 (replaced, not duplicated)", before, after)
	}
	// Provenance recorded so future replacement stays scoped.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM history WHERE source_path = ''`); n != 0 {
		t.Errorf("%d history rows missing source_path", n)
	}
}

func queryString(t *testing.T, store *db.Store, q string, args ...any) string {
	t.Helper()
	var s string
	if err := store.DB().QueryRowContext(context.Background(), q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}

// Cancelling mid-run (Ctrl-C over a long first pass) must still close the
// ingest_runs row. Writing the outcome with the cancelled context made
// both statements fail instantly and left the row at 'running' forever,
// so `ccpeek ingest` and `ccpeek doctor` reported a run in flight that
// had died minutes ago.
func TestCancelledRunIsRecordedAsFailed(t *testing.T) {
	runner, store := newRunner(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	opts := fixtureOptions(t)
	// Cancel from inside the pipeline, once it is definitely past StartRun.
	opts.Progress = func(p Progress) {
		if !p.Root {
			cancel()
		}
	}

	if _, err := runner.Run(ctx, opts); err == nil {
		t.Fatal("Run returned nil error after cancellation")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}

	if n := queryInt(t, store, `SELECT COUNT(*) FROM ingest_runs WHERE status = 'running'`); n != 0 {
		t.Errorf("%d run(s) left at 'running' after cancellation", n)
	}
	status := queryString(t, store, `SELECT status FROM ingest_runs ORDER BY id DESC LIMIT 1`)
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
	if msg := queryString(t, store, `SELECT error_message FROM ingest_runs ORDER BY id DESC LIMIT 1`); msg == "" {
		t.Error("failed run recorded no error message")
	}
	if fin := queryString(t, store, `SELECT COALESCE(finished_at, '') FROM ingest_runs ORDER BY id DESC LIMIT 1`); fin == "" {
		t.Error("failed run has no finished_at")
	}
}

// A tail parse that has to fall back to a full parse must report what it
// actually committed, once. The sink used to increment the run's counters
// as records streamed, so the rolled-back attempt still inflated
// records_indexed, the "Indexed N sources" summary, and the diagnostics.
func TestTailFallbackCountsRecordsOnce(t *testing.T) {
	runner, store := newRunner(t)
	ctx := context.Background()

	tmp := t.TempDir()
	copyDir(t, fixturePath(t, "claude-code"), filepath.Join(tmp, "claude-code"))
	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{filepath.Join(tmp, "claude-code")}

	if _, err := runner.Run(ctx, opts); err != nil {
		t.Fatal(err)
	}

	// Rewrite the prefix in place so the stored cursor no longer verifies:
	// the append path is attempted, rejected, and the full parse re-runs.
	target := filepath.Join(tmp, "claude-code", "projects", "-home-u-demo-api",
		"22222222-aaaa-bbbb-cccc-222222222222.jsonl")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := []byte(strings.Replace(string(data), `"gitBranch":"feat/limits"`, `"gitBranch":"feat/limitz"`, 1))
	if len(rewritten) != len(data) || string(rewritten) == string(data) {
		t.Fatal("fixture rewrite did not apply as a same-length prefix change")
	}
	if err := os.WriteFile(target, rewritten, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}

	// The one re-parsed session is the only thing that landed, so the
	// report must match the rows that source actually owns.
	var wantMessages, wantTools int
	if err := store.DB().QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM messages m
		        JOIN sessions s ON s.id = m.session_id WHERE s.source_path = ?),
		       (SELECT COUNT(*) FROM tool_calls tc
		        JOIN sessions s ON s.id = tc.session_id WHERE s.source_path = ?)`,
		target, target).Scan(&wantMessages, &wantTools); err != nil {
		t.Fatal(err)
	}
	if report.Sessions != 1 {
		t.Errorf("report sessions = %d, want 1 (the attempt must not be counted too)", report.Sessions)
	}
	if report.Messages != wantMessages {
		t.Errorf("report messages = %d, want %d (rows actually committed)", report.Messages, wantMessages)
	}
	if report.ToolCalls != wantTools {
		t.Errorf("report tool calls = %d, want %d", report.ToolCalls, wantTools)
	}

	// records_indexed carries the same figure into run telemetry.
	indexed := queryInt(t, store,
		`SELECT records_indexed FROM ingest_runs ORDER BY id DESC LIMIT 1`)
	if want := report.Sessions + report.Messages + report.ToolCalls + report.Artifacts + report.History; indexed != want {
		t.Errorf("records_indexed = %d, want %d", indexed, want)
	}
}

// A parse that fails partway rolls its writes back, so the run report
// must not count the records it staged — records_indexed claims committed
// rows. The line-level diagnostics DO survive: they are what makes the
// failure debuggable, and no retry will re-emit them.
//
// Pi is the adapter that exposes this: it emits a warning for a malformed
// line before its session header exists, then fails hard when the header
// never arrives.
func TestFailedParsePublishesIssuesButNoCounts(t *testing.T) {
	runner, store := newRunner(t)
	ctx := context.Background()

	tmp := t.TempDir()
	root := filepath.Join(tmp, "pi")
	sessDir := filepath.Join(root, "sessions", "--home-u-x--")
	if err := os.MkdirAll(sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(sessDir, "2026-07-01T10-00-00_bbbbbbbb-1111-2222-3333-444444444444.jsonl")
	// Line 1 is unparseable (a warning); line 2 is a valid entry that is
	// not a session header, which Pi rejects for the whole source.
	body := "{ not json\n" + `{"type":"message","id":"m1"}` + "\n"
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{filepath.Join(tmp, "no-claude-here")}
	opts.ConfigRoots[pi.Slug] = []string{root}

	report, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}

	if report.Sessions != 0 || report.Messages != 0 || report.ToolCalls != 0 {
		t.Errorf("report counted %d sessions / %d messages / %d tool calls from a rolled-back parse, want 0",
			report.Sessions, report.Messages, report.ToolCalls)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("precondition: %d session rows committed, expected the parse to roll back", n)
	}
	if n := queryInt(t, store,
		`SELECT records_indexed FROM ingest_runs ORDER BY id DESC LIMIT 1`); n != 0 {
		t.Errorf("records_indexed = %d, want 0", n)
	}

	// The warning survives, alongside the pipeline's own per-source error.
	var warn, hard int
	for _, is := range report.Issues {
		if is.SourcePath != target {
			continue
		}
		if is.Severity == canon.SeverityWarn {
			warn++
		} else {
			hard++
		}
	}
	if warn != 1 {
		t.Errorf("warnings for the bad line = %d, want 1; issues: %+v", warn, report.Issues)
	}
	if hard != 1 {
		t.Errorf("hard errors for the source = %d, want 1", hard)
	}
	if n := queryInt(t, store,
		`SELECT COUNT(*) FROM ingest_issues WHERE source_path = ? AND severity = 'warn'`, target); n != 1 {
		t.Errorf("stored warnings = %d, want 1", n)
	}
}

// Re-running over the same broken source must not accumulate duplicate
// diagnostics run over run — each run records its own, once.
func TestFailedParseIssuesDoNotAccumulateWithinARun(t *testing.T) {
	runner, store := newRunner(t)
	ctx := context.Background()

	tmp := t.TempDir()
	root := filepath.Join(tmp, "claude-code")
	if err := os.MkdirAll(filepath.Join(root, "projects", "-home-u-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "projects", "-home-u-x",
		"aaaaaaaa-1111-2222-3333-444444444444.jsonl")
	good := `{"type":"user","uuid":"u1","sessionId":"aaaaaaaa-1111-2222-3333-444444444444","timestamp":"2026-07-01T10:00:00Z","cwd":"/home/u/x","message":{"role":"user","content":"hello"}}`
	if err := os.WriteFile(target, []byte(good+"\n{ not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := fixtureOptions(t)
	opts.ConfigRoots[claude.Slug] = []string{root}
	opts.ConfigRoots[pi.Slug] = []string{filepath.Join(tmp, "no-pi-here")}

	report, err := runner.Run(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	parseWarnings := 0
	for _, is := range report.Issues {
		if is.SourcePath == target {
			parseWarnings++
		}
	}
	if parseWarnings != 1 {
		t.Errorf("issues for the bad line = %d, want 1; got %+v", parseWarnings, report.Issues)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM ingest_issues WHERE source_path = ?`, target); n != 1 {
		t.Errorf("stored issues = %d, want 1", n)
	}
	// The good line still landed — a bad line is not a bad file.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM messages`); n != 1 {
		t.Errorf("messages = %d, want 1", n)
	}
}
