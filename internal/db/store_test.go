package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v2.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestOpenFreshCreatesLatestSchema(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	v, err := s.readVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if v != schemaVersion {
		t.Fatalf("fresh db at v%d, want v%d", v, schemaVersion)
	}

	for _, table := range []string{
		"agents", "sessions", "session_relations", "pending_relations",
		"messages", "message_usage", "tool_calls", "artifacts",
		"artifact_sessions", "workspaces", "session_workspaces", "history",
		"source_files", "ingest_runs", "ingest_issues", "rollup_usage_daily",
		"scan_findings", "user_annotations", "search_docs", "search_fts",
	} {
		var name string
		err := s.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (slug) VALUES ('claude-code')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s.Close()

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	var n int
	if err := s2.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agents`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("agents after reopen = %d (err %v), want 1", n, err)
	}
}

// TestReadsDontQueueBehindWrites is the serve-first responsiveness
// contract: with the writer connection held by an open transaction (a
// watch-mode ingest, a rollup regen, the secret scan's write phase), the
// read pool must still answer immediately.
func TestReadsDontQueueBehindWrites(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (slug) VALUES ('claude-code')`); err != nil {
		t.Fatal(err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agents (slug) VALUES ('pi')`); err != nil {
		t.Fatal(err)
	}

	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var n int
	if err := s.ReadDB().QueryRowContext(readCtx,
		`SELECT COUNT(*) FROM agents`).Scan(&n); err != nil {
		t.Fatalf("read blocked behind open write tx: %v", err)
	}
	// WAL snapshot isolation: the uncommitted insert is invisible.
	if n != 1 {
		t.Fatalf("read saw %d agents, want 1 (committed state only)", n)
	}
}

// TestMigrationInvariant pins the bookkeeping the upgrade path relies
// on: every version between baseVersion and schemaVersion has exactly
// one registered migration (pre-release both are equal and the slice is
// empty).
func TestV13CostMigrationPreservesArchiveAndAddsColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v13.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// Use the real table relationships rather than column stubs: later
	// migrations legitimately need to recover data through those relationships.
	if _, err := raw.ExecContext(ctx, derivedSchema+derivedVirtualSchema+userSchema); err != nil {
		t.Fatal(err)
	}
	for table, columns := range map[string][]string{
		"messages":           {"provider", "text_content", "usage_request_id"},
		"tool_calls":         {"result_content"},
		"message_usage":      {"cache_write_1h_tokens", "reported_cost_nanos"},
		"source_files":       {"parse_version"},
		"rollup_usage_daily": {"unpriced_input_tokens", "unpriced_output_tokens", "unpriced_cache_read_tokens", "unpriced_cache_write_tokens", "cost_nanos", "cost_reported_nanos", "cost_estimated_nanos"},
	} {
		for _, column := range columns {
			if _, err := raw.ExecContext(ctx, "ALTER TABLE "+table+" DROP COLUMN "+column); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, q := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta VALUES ('schema_version', '13')`,
		`INSERT INTO agents(id,slug) VALUES(1,'claude-code')`,
		`INSERT INTO sessions(id,agent_id,external_id) VALUES(1,1,'retained')`,
		`INSERT INTO messages(id,session_id,seq,role,content_id) VALUES(1,1,0,'assistant','request-content')`,
		`INSERT INTO search_docs(session_id,doc_type,seq,text_content) VALUES(1,'message',0,'retained text')`,
		`INSERT INTO message_usage(message_id,reported_cost_usd,request_id) VALUES (1,0.125,'req1')`,
		`INSERT INTO source_files(path,agent_id,content_hash,indexed_at) VALUES ('kept',1,'h','')`,
		`INSERT INTO rollup_usage_daily(day,agent_id) VALUES ('2026-01-01',1)`,
		`INSERT INTO rollup_session_days(day,agent_id,session_id) VALUES ('2026-01-01',1,1)`,
	} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			raw.Close()
			t.Fatalf("seed v13: %v (%s)", err, q)
		}
	}
	raw.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := count(t, s, `SELECT COUNT(*) FROM messages`); n != 1 {
		t.Errorf("archive messages = %d, want preserved", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM rollup_usage_daily`); n != 0 {
		t.Errorf("rollups = %d, want invalidated", n)
	}
	var reportedNanos int64
	if err := s.db.QueryRowContext(ctx, `SELECT reported_cost_nanos FROM message_usage WHERE message_id = 1`).Scan(&reportedNanos); err != nil || reportedNanos != 125_000_000 {
		t.Errorf("reported cost backfill = %d, %v; want 125000000", reportedNanos, err)
	}
	for table, column := range map[string]string{
		"messages": "provider", "message_usage": "reported_cost_nanos",
		"source_files": "parse_version", "rollup_usage_daily": "cost_nanos",
	} {
		if n := count(t, s, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column); n != 1 {
			t.Errorf("%s.%s missing after migration", table, column)
		}
	}
}

func TestMigrationInvariant(t *testing.T) {
	if len(migrations) != schemaVersion-baseVersion {
		t.Fatalf("len(migrations) = %d, want schemaVersion-baseVersion = %d",
			len(migrations), schemaVersion-baseVersion)
	}
	if baseVersion > schemaVersion {
		t.Fatalf("baseVersion %d > schemaVersion %d", baseVersion, schemaVersion)
	}
}

// TestOpenPreBaseVersionFails: a database older than the upgrade
// baseline refuses to open with re-create instructions instead of being
// silently rebuilt or half-upgraded.
func TestOpenPreBaseVersionFails(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	if err := s.writeVersion(ctx, baseVersion-1); err != nil {
		t.Fatalf("writeVersion: %v", err)
	}
	s.Close()

	if _, err := Open(ctx, path); !errors.Is(err, ErrNoMigrationPath) {
		t.Fatalf("open pre-base db error = %v, want ErrNoMigrationPath", err)
	}
}

// TestMigrationInfraApplies exercises the machinery that stays dormant
// until the v2.0 release: a registered step runs in a transaction,
// stamps the version, preserves retained rows, and a failing step
// changes nothing.
func TestMigrationInfraApplies(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (agent_id, external_id, source_path)
		VALUES (1, 'ghost-session', '/gone/ghost.jsonl')`); err != nil {
		t.Fatal(err)
	}

	saved := migrations
	defer func() { migrations = saved }()

	// A failing step must roll back without stamping.
	migrations = []migration{func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `CREATE TABLE migration_marker (id INTEGER)`); err != nil {
			return err
		}
		return errors.New("boom")
	}}
	if err := s.applyMigration(ctx, baseVersion); err == nil {
		t.Fatal("failing migration reported success")
	}
	if v, _ := s.readVersion(ctx); v != schemaVersion {
		t.Fatalf("version after failed migration = %d, want %d", v, schemaVersion)
	}
	if _, err := s.db.ExecContext(ctx, `SELECT * FROM migration_marker`); err == nil {
		t.Fatal("failed migration left its table behind (no rollback)")
	}

	// A succeeding step applies and stamps.
	migrations = []migration{func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN marker TEXT NOT NULL DEFAULT ''`)
		return err
	}}
	if err := s.applyMigration(ctx, baseVersion); err != nil {
		t.Fatalf("applyMigration: %v", err)
	}
	if v, _ := s.readVersion(ctx); v != baseVersion+1 {
		t.Fatalf("version after migration = %d, want %d", v, baseVersion+1)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sessions WHERE external_id = 'ghost-session' AND marker = ''`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retained session after migration = %d (err %v), want 1", n, err)
	}
}

func TestOpenFutureSchemaFails(t *testing.T) {
	s, path := openTemp(t)
	ctx := context.Background()
	if err := s.writeVersion(ctx, schemaVersion+10); err != nil {
		t.Fatalf("writeVersion: %v", err)
	}
	s.Close()

	_, err := Open(ctx, path)
	if !errors.Is(err, ErrFutureSchema) {
		t.Fatalf("err = %v, want ErrFutureSchema", err)
	}
}

func TestCascadeDeleteSessionOwnsChildren(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`)
	mustExec(`INSERT INTO sessions (id, agent_id, external_id) VALUES (10, 1, 'sess-a')`)
	mustExec(`INSERT INTO messages (id, session_id, seq, role) VALUES (100, 10, 0, 'assistant')`)
	mustExec(`INSERT INTO message_usage (message_id, input_tokens, output_tokens) VALUES (100, 5, 7)`)
	mustExec(`INSERT INTO tool_calls (session_id, seq, name) VALUES (10, 0, 'Bash')`)

	mustExec(`DELETE FROM sessions WHERE id = 10`)

	for _, q := range []string{
		`SELECT COUNT(*) FROM messages`,
		`SELECT COUNT(*) FROM message_usage`,
		`SELECT COUNT(*) FROM tool_calls`,
	} {
		var n int
		if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Errorf("%s = %d after session delete, want 0", q, n)
		}
	}
}

func TestResetDerivedPreservesUserState(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (agent_id, external_id) VALUES (1, 'sess-a')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		 VALUES ('scan_finding', 'claude-code/sess-a/rule-x', 'scan_ignore', '{}', '2026-07-10T00:00:00Z')`); err != nil {
		t.Fatalf("seed annotation: %v", err)
	}

	if err := s.ResetDerived(ctx); err != nil {
		t.Fatalf("ResetDerived: %v", err)
	}

	var sessions, annotations int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_annotations`).Scan(&annotations); err != nil {
		t.Fatalf("count annotations: %v", err)
	}
	if sessions != 0 {
		t.Errorf("sessions = %d after reset, want 0", sessions)
	}
	if annotations != 1 {
		t.Errorf("user_annotations = %d after reset, want 1 (must survive rebuild)", annotations)
	}

	// The store must be fully usable after a reset.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO agents (id, slug) VALUES (1, 'claude-code')`); err != nil {
		t.Fatalf("reinsert after reset: %v", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO search_docs (doc_type, seq, title, text_content) VALUES ('m', 0, 't', 'hello world')`); err != nil {
		t.Fatalf("search doc insert after reset: %v", err)
	}
}

func TestFTSRoundTrip(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO search_docs (doc_type, seq, title, text_content)
		 VALUES ('message', 3, 'greeting', 'hello session world')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var docType, snip string
	var seq int
	err := s.db.QueryRowContext(ctx, `
		SELECT d.doc_type, d.seq, snippet(search_fts, 0, '[', ']', '…', 5)
		FROM search_fts
		JOIN search_docs d ON d.id = search_fts.rowid
		WHERE search_fts MATCH 'session' ORDER BY rank LIMIT 1`).
		Scan(&docType, &seq, &snip)
	if err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if docType != "message" || seq != 3 {
		t.Errorf("locator = %s/%d", docType, seq)
	}
	if snip == "" {
		t.Error("empty snippet")
	}

	// Trigger-maintained consistency: deleting the doc removes the hit.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_docs`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'session'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("fts hits after delete = %d, want 0", n)
	}
}

// The column is called canonical_path, so it must hold one. A trailing
// separator (or a "." segment) used to split one project's sessions
// across two Projects entries, and ?project= then matched only one.
func TestWorkspaceCanonicalization(t *testing.T) {
	for _, tc := range []struct{ in, wantPath, wantName string }{
		{"/home/u/proj", "/home/u/proj", "proj"},
		{"/home/u/proj/", "/home/u/proj", "proj"},
		{"/home/u/proj//", "/home/u/proj", "proj"},
		{"/home/u/./proj", "/home/u/proj", "proj"},
		{"/home/u/x/../proj", "/home/u/proj", "proj"},
		{"  /home/u/proj  ", "/home/u/proj", "proj"},
		{"/", "/", "/"},
		{"", "", ""},
		{"   ", "", ""},
		{"relative/dir", "relative/dir", "dir"},
	} {
		if got := CanonicalWorkspace(tc.in); got != tc.wantPath {
			t.Errorf("CanonicalWorkspace(%q) = %q, want %q", tc.in, got, tc.wantPath)
		}
		if got := WorkspaceDisplayName(CanonicalWorkspace(tc.in)); got != tc.wantName {
			t.Errorf("WorkspaceDisplayName(%q) = %q, want %q", tc.in, got, tc.wantName)
		}
	}
}

// Sessions whose cwd differs only by a trailing separator belong to the
// SAME workspace, and every workspace keeps a usable display name.
func TestRegenerateWorkspacesGroupsEquivalentPaths(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	w, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, cwd := range []string{"/home/u/proj", "/home/u/proj/", "/home/u/other/"} {
		sess := canon.Session{
			Agent:      "claude-code",
			ExternalID: fmt.Sprintf("s%d", i),
			CWD:        cwd,
			SourcePath: "/x.jsonl",
		}
		if _, err := w.UpsertSession(sess, "h"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.RegenerateWorkspaces(ctx); err != nil {
		t.Fatalf("RegenerateWorkspaces: %v", err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspaces`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("workspaces = %d, want 2 (the trailing slash is not a second project)", n)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT canonical_path, display_name FROM workspaces ORDER BY canonical_path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var p, d string
		if err := rows.Scan(&p, &d); err != nil {
			t.Fatal(err)
		}
		if d == "" {
			t.Errorf("workspace %q has an empty display name", p)
		}
		got[p] = d
	}
	if got["/home/u/proj"] != "proj" || got["/home/u/other"] != "other" {
		t.Errorf("workspaces = %v", got)
	}

	// Both sessions in /home/u/proj are members of the one workspace.
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_workspaces sw
		JOIN workspaces w ON w.id = sw.workspace_id
		WHERE w.canonical_path = '/home/u/proj'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("members of /home/u/proj = %d, want 2", n)
	}
}

// A long-lived watch process opens a run row per debounce fire; the
// history is trimmed so neither ingest_runs nor its issues grow forever.
func TestTrimRunsKeepsTheNewestAndCascadesIssues(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	var ids []int64
	for i := range 12 {
		id, err := s.StartRun(ctx, "incremental", "[]")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		if err := s.InsertIssues(ctx, id, []canon.Issue{{
			Severity: canon.SeverityWarn, Category: "parse",
			Detail: fmt.Sprintf("run %d", i),
		}}); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := s.TrimRuns(ctx, 5)
	if err != nil {
		t.Fatalf("TrimRuns: %v", err)
	}
	if removed != 7 {
		t.Errorf("removed = %d, want 7", removed)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ingest_runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("runs = %d, want 5", n)
	}
	// Their issues went with them.
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ingest_issues`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("issues = %d, want 5 (one per surviving run)", n)
	}
	// The survivors are the newest.
	var oldest int64
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(id) FROM ingest_runs`).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	if oldest != ids[len(ids)-5] {
		t.Errorf("oldest surviving run = %d, want %d", oldest, ids[len(ids)-5])
	}

	// Trimming below the retained count is a no-op.
	if removed, err := s.TrimRuns(ctx, 100); err != nil || removed != 0 {
		t.Errorf("TrimRuns(100) = %d, %v; want 0, nil", removed, err)
	}
}

// ResetDerived must leave a COMPLETE schema behind, search index
// included. Dropping search_docs/search_fts outside the transaction that
// recreated them meant a failure between the phases left every derived
// table intact with no search index — a state Open never repairs, since
// these tables are only ever created here.
func TestResetDerivedRebuildsTheSearchIndex(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	seed := func(text string) {
		t.Helper()
		w, err := s.BeginWrite(ctx)
		if err != nil {
			t.Fatal(err)
		}
		id, err := w.UpsertSession(canon.Session{
			Agent: "claude-code", ExternalID: "s1", SourcePath: "/x.jsonl",
		}, "h")
		if err != nil {
			t.Fatal(err)
		}
		if err := w.InsertSearchDoc(id, 0, "message", 0, "", text); err != nil {
			t.Fatal(err)
		}
		if err := w.Commit(); err != nil {
			t.Fatal(err)
		}
	}

	seed("findable before the reset")
	if n := count(t, s, `SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'findable'`); n != 1 {
		t.Fatalf("precondition: %d hits before reset, want 1", n)
	}

	if err := s.ResetDerived(ctx); err != nil {
		t.Fatalf("ResetDerived: %v", err)
	}

	// Both halves of the pair exist and are empty…
	for _, table := range []string{"search_docs", "search_fts"} {
		if n := count(t, s, `SELECT COUNT(*) FROM `+table); n != 0 {
			t.Errorf("%s has %d rows after reset, want 0", table, n)
		}
	}
	// …and the triggers that keep them in sync were recreated with them,
	// so newly indexed content is searchable again.
	seed("findable after the reset")
	if n := count(t, s, `SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'findable'`); n != 1 {
		t.Errorf("post-reset hits = %d, want 1 — the FTS triggers did not survive", n)
	}

	// Every derived table is back, and a second reset is a no-op rather
	// than an error.
	if err := s.ResetDerived(ctx); err != nil {
		t.Fatalf("second ResetDerived: %v", err)
	}
	for _, table := range derivedTables {
		if _, err := s.db.ExecContext(ctx, `SELECT COUNT(*) FROM `+table); err != nil {
			t.Errorf("derived table %s missing after reset: %v", table, err)
		}
	}
}
