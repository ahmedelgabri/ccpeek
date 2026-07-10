package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
	// Artifact links resolved against ingested sessions.
	if n := queryInt(t, store, `SELECT COUNT(*) FROM artifact_sessions`); n != 4 {
		t.Errorf("artifact_sessions = %d, want 4 (todo, task, file-history, facet)", n)
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
