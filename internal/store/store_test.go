package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func copyMigrationFixture(t *testing.T, name string) string {
	t.Helper()

	src := filepath.Join("testdata", "migrations", name)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", src, err)
	}

	dst := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("write temp fixture copy %s: %v", dst, err)
	}
	return dst
}

func assertSchemaVersion(t *testing.T, s *Store, want int) {
	t.Helper()

	var got int
	if err := s.db.GetContext(context.Background(), &got, `SELECT value FROM meta WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if got != want {
		t.Fatalf("expected schema version %d, got %d", want, got)
	}
}

type migrationFixtureMatrixCase struct {
	fixture         string
	projectDirName  string
	canonicalPath   string
	toolCallCount   int
	toolResultText  string
	commands        []string
	searchDocCounts map[string]int
}

func openMigratedFixture(t *testing.T, fixture string) *Store {
	t.Helper()

	s, err := Open(context.Background(), copyMigrationFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	assertSchemaVersion(t, s, schemaVersion)
	return s
}

func assertMigrationFixtureMatrixCase(t *testing.T, tc migrationFixtureMatrixCase) *Store {
	t.Helper()

	s := openMigratedFixture(t, tc.fixture)
	assertProjectCanonicalPath(t, s, tc.projectDirName, tc.canonicalPath)
	if tc.toolCallCount >= 0 {
		assertToolCallCount(t, s, tc.toolCallCount)
	}
	if tc.toolResultText != "" {
		assertFirstToolResultText(t, s, tc.toolResultText)
	}
	if tc.commands != nil {
		assertListedCommands(t, s, tc.commands...)
	}
	for groupType, want := range tc.searchDocCounts {
		assertSearchDocumentCount(t, s, groupType, want)
	}
	return s
}

func assertProjectCanonicalPath(t *testing.T, s *Store, dirName, want string) {
	t.Helper()

	project, err := s.GetProject(context.Background(), dirName)
	if err != nil {
		t.Fatal(err)
	}
	if project.CanonicalPath != want {
		t.Fatalf("expected canonical path %q for %s, got %q", want, dirName, project.CanonicalPath)
	}
}

func assertToolCallCount(t *testing.T, s *Store, want int) {
	t.Helper()

	var got int
	if err := s.db.GetContext(context.Background(), &got, `SELECT COUNT(*) FROM tool_calls`); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected %d tool calls after fixture migration, got %d", want, got)
	}
}

func assertFirstToolResultText(t *testing.T, s *Store, want string) {
	t.Helper()

	var got string
	if err := s.db.GetContext(context.Background(), &got, `SELECT result_text FROM tool_calls LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected first tool result text %q, got %q", want, got)
	}
}

func assertListedCommands(t *testing.T, s *Store, want ...string) {
	t.Helper()

	commands, total, err := s.ListCommands(context.Background(), len(want)+10, 0, CommandFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != len(want) {
		t.Fatalf("expected %d commands after fixture migration, got %d", len(want), total)
	}
	if len(commands) != len(want) {
		t.Fatalf("expected %d listed commands, got %d", len(want), len(commands))
	}
	for i, wantCommand := range want {
		if commands[i].Command != wantCommand {
			t.Fatalf("expected command %d to be %q, got %q", i, wantCommand, commands[i].Command)
		}
	}
}

func assertSearchDocumentCount(t *testing.T, s *Store, groupType string, want int) {
	t.Helper()

	var got int
	if err := s.db.GetContext(context.Background(), &got, `SELECT COUNT(*) FROM search_documents_fts WHERE group_type = ?`, groupType); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected %d search documents for %s, got %d", want, groupType, got)
	}
}

func assertTableExists(t *testing.T, s *Store, table string) {
	t.Helper()

	var count int
	if err := s.db.GetContext(context.Background(), &count, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected table %s to exist", table)
	}
}

func assertColumnExists(t *testing.T, s *Store, table, column string) {
	t.Helper()

	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?`, table)
	if err := s.db.GetContext(context.Background(), &count, query, column); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected column %s.%s to exist", table, column)
	}
}

func assertColumnMissing(t *testing.T, s *Store, table, column string) {
	t.Helper()

	var count int
	query := fmt.Sprintf(`SELECT COUNT(*) FROM pragma_table_info('%s') WHERE name = ?`, table)
	if err := s.db.GetContext(context.Background(), &count, query, column); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected column %s.%s to be absent", table, column)
	}
}

func TestResetDropsAllTables(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert data into tables that depend on sessions (commands, messages)
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('p', 'P')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions (session_id, project_id, source_path) VALUES ('s1', 1, '/src')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO commands (session_id, seq, command, timestamp) VALUES (1, 0, 'echo hi', '2025-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages (session_id, seq, type, content) VALUES (1, 0, 'user', '"hello"')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Reset should succeed (commands table dropped before sessions)
	if err := s.Reset(ctx); err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify tables are recreated and empty
	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM sessions`); err != nil {
		t.Fatal("sessions table missing after Reset:", err)
	}
	if count != 0 {
		t.Errorf("expected 0 sessions after Reset, got %d", count)
	}
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM commands`); err != nil {
		t.Fatal("commands table missing after Reset:", err)
	}
}

func TestMigrateRecoversMissingTables(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Simulate corruption: drop a table but leave schema version intact
	if _, err := s.db.ExecContext(ctx, `DROP TABLE messages`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TABLE messages_fts`); err != nil {
		t.Fatal(err)
	}

	// Re-running migrate should recover the missing tables
	if err := s.migrate(ctx); err != nil {
		t.Fatalf("migrate failed to recover: %v", err)
	}

	// Verify the table was recreated
	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM messages`); err != nil {
		t.Fatal("messages table not recovered:", err)
	}
}

func TestMigrateFixtureBackfillsDerivedData(t *testing.T) {
	s := assertMigrationFixtureMatrixCase(t, migrationFixtureMatrixCase{
		fixture:        "v10-derived-data.db",
		projectDirName: "-Users-me-my-project",
		canonicalPath:  "/Users/me/my-project",
		toolCallCount:  1,
		toolResultText: "ok",
		commands:       []string{"echo hi"},
		searchDocCounts: map[string]int{
			searchGroupCommands:      1,
			searchGroupConversations: 2,
		},
	})
	defer s.Close()
}

func TestMigrateFixturePreservesEarliestSupportedData(t *testing.T) {
	ctx := context.Background()
	s := assertMigrationFixtureMatrixCase(t, migrationFixtureMatrixCase{
		fixture:        "v4-earliest-supported.db",
		projectDirName: "-Users-me-earliest-project",
		canonicalPath:  "/Users/me/earliest-project",
		toolCallCount:  1,
		commands:       []string{"echo earliest"},
	})
	defer s.Close()

	assertColumnExists(t, s, "sessions", "source_path")

	var sourcePath string
	if err := s.db.GetContext(ctx, &sourcePath, `SELECT source_path FROM sessions WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if sourcePath != "" {
		t.Fatalf("expected migrated sessions.source_path default to be empty, got %q", sourcePath)
	}

	assertColumnExists(t, s, "source_files", "content_hash")
	assertColumnMissing(t, s, "source_files", "mtime_ns")

	var sourceFile struct {
		ContentHash string `db:"content_hash"`
		IndexedAt   string `db:"indexed_at"`
	}
	if err := s.db.GetContext(ctx, &sourceFile, `SELECT content_hash, indexed_at FROM source_files WHERE path = ?`, "/src/earliest-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if sourceFile.ContentHash != "" {
		t.Fatalf("expected migrated source_files.content_hash to default empty, got %q", sourceFile.ContentHash)
	}
	if sourceFile.IndexedAt != "2024-01-01T00:00:10Z" {
		t.Fatalf("expected source_files.indexed_at to be preserved, got %q", sourceFile.IndexedAt)
	}
}

func TestMigrateFixturePreservesV5SourcePathAndPlanData(t *testing.T) {
	ctx := context.Background()
	s := assertMigrationFixtureMatrixCase(t, migrationFixtureMatrixCase{
		fixture:        "v5-source-path-and-plan-data.db",
		projectDirName: "-Users-me-v5-project",
		canonicalPath:  "/Users/me/v5-project",
		toolCallCount:  1,
		toolResultText: "v5 ok",
		commands:       []string{"echo v5"},
		searchDocCounts: map[string]int{
			searchGroupCommands: 1,
			searchGroupPlans:    1,
		},
	})
	defer s.Close()

	assertTableExists(t, s, "commands")
	assertTableExists(t, s, "scan_findings")
	assertColumnExists(t, s, "scan_findings", "ignored")
	assertColumnExists(t, s, "sessions", "source_path")
	assertColumnExists(t, s, "plans", "source_path")

	var sessionSourcePath string
	if err := s.db.GetContext(ctx, &sessionSourcePath, `SELECT source_path FROM sessions WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if sessionSourcePath != "/src/v5-session.jsonl" {
		t.Fatalf("expected sessions.source_path to be preserved, got %q", sessionSourcePath)
	}

	var plan struct {
		Title      string `db:"title"`
		SourcePath string `db:"source_path"`
	}
	if err := s.db.GetContext(ctx, &plan, `SELECT title, source_path FROM plans WHERE file_name = ?`, "v5-plan.md"); err != nil {
		t.Fatal(err)
	}
	if plan.Title != "V5 Plan" {
		t.Fatalf("expected plan title to be preserved, got %q", plan.Title)
	}
	if plan.SourcePath != "/src/v5-plan.md" {
		t.Fatalf("expected plans.source_path to be preserved, got %q", plan.SourcePath)
	}

	var contentHash string
	if err := s.db.GetContext(ctx, &contentHash, `SELECT content_hash FROM source_files WHERE path = ?`, "/src/v5-session.jsonl"); err != nil {
		t.Fatal(err)
	}
	if contentHash != "v5-hash" {
		t.Fatalf("expected source_files.content_hash to be preserved, got %q", contentHash)
	}
}

func TestMigrateFixtureUpgradesLegacySessionsAndScanFindings(t *testing.T) {
	ctx := context.Background()
	s := assertMigrationFixtureMatrixCase(t, migrationFixtureMatrixCase{
		fixture:        "v7-legacy-sessions-and-scan-findings.db",
		projectDirName: "-Users-me-legacy-project",
		canonicalPath:  "/Users/me/legacy-project",
		toolCallCount:  1,
		commands:       []string{"echo legacy"},
		searchDocCounts: map[string]int{
			searchGroupCommands: 1,
		},
	})
	defer s.Close()

	assertColumnExists(t, s, "scan_findings", "ignored")

	var ignored int
	if err := s.db.GetContext(ctx, &ignored, `SELECT ignored FROM scan_findings WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if ignored != 0 {
		t.Fatalf("expected migrated scan finding ignored=0, got %d", ignored)
	}

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := s.InsertProject(ctx, tx, "legacy-project-copy", "Legacy Project Copy", "/Users/me/legacy-project-copy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertSession(ctx, tx, projectID, model.SessionEntry{SessionID: "legacy-session"}, "/src/legacy-project-copy/legacy-session.jsonl")
	if err != nil {
		t.Fatalf("expected duplicate session id across projects to succeed after migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var sessionCount int
	if err := s.db.GetContext(ctx, &sessionCount, `SELECT COUNT(*) FROM sessions WHERE session_id = 'legacy-session'`); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 2 {
		t.Fatalf("expected 2 sessions with migrated duplicate session_id support, got %d", sessionCount)
	}
}

func TestMigrateFixtureRelaxesLegacySessionUniqueness(t *testing.T) {
	ctx := context.Background()
	s := assertMigrationFixtureMatrixCase(t, migrationFixtureMatrixCase{
		fixture:        "v8-session-uniqueness.db",
		projectDirName: "-Users-me-v8-project",
		canonicalPath:  "/Users/me/v8-project",
		toolCallCount:  1,
		commands:       []string{"echo v8"},
	})
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := s.InsertProject(ctx, tx, "v8-project-copy", "V8 Project Copy", "/Users/me/v8-project-copy")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertSession(ctx, tx, projectID, model.SessionEntry{SessionID: "shared-session"}, "/src/v8-project-copy/shared-session.jsonl")
	if err != nil {
		t.Fatalf("expected duplicate session id across projects to succeed after v8 migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err = s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InsertSession(ctx, tx, 1, model.SessionEntry{SessionID: "shared-session"}, "/src/v8-project/shared-session-again.jsonl")
	if err == nil {
		t.Fatal("expected duplicate session id within the same project to fail after v8 migration")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var sessionCount int
	if err := s.db.GetContext(ctx, &sessionCount, `SELECT COUNT(*) FROM sessions WHERE session_id = 'shared-session'`); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 2 {
		t.Fatalf("expected 2 sessions with migrated per-project uniqueness, got %d", sessionCount)
	}
}

func TestBackfillSearchIndexRepopulatesExistingData(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InsertPlan(ctx, tx, model.PlanEntry{FileName: "alpha.md", Title: "Alpha Architecture", SizeBytes: 12}, "Step one", "/src/alpha.md"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := s.backfillSearchIndex(ctx); err != nil {
		t.Fatal(err)
	}

	groups, err := s.SearchAll(ctx, "Architecture", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) == 0 {
		t.Fatal("expected search groups after backfill")
	}

	foundPlan := false
	for _, g := range groups {
		if g.Type != "Plans" {
			continue
		}
		if len(g.Hits) == 0 {
			t.Fatal("expected plan hits after backfill")
		}
		foundPlan = true
		break
	}
	if !foundPlan {
		t.Fatal("expected Plans group after backfill")
	}
}

func TestOpenRecoversDamagedVersionedDerivedTables(t *testing.T) {
	ctx := context.Background()
	s := assertMigrationFixtureMatrixCase(t, migrationFixtureMatrixCase{
		fixture:        "v14-damaged-derived-tables.db",
		projectDirName: "-Users-me-damaged-project",
		canonicalPath:  "/Users/me/damaged-project",
		toolCallCount:  1,
		toolResultText: "damaged ok",
		commands:       []string{"echo damaged"},
		searchDocCounts: map[string]int{
			searchGroupCommands:      1,
			searchGroupConversations: 2,
			searchGroupPlans:         1,
		},
	})
	defer s.Close()

	assertTableExists(t, s, "commands")
	assertTableExists(t, s, "tool_calls")
	assertTableExists(t, s, "search_documents_fts")

	var legacyCommandRows int
	if err := s.db.GetContext(ctx, &legacyCommandRows, `SELECT COUNT(*) FROM commands`); err != nil {
		t.Fatal(err)
	}
	if legacyCommandRows != 0 {
		t.Fatalf("expected recreated legacy commands table to remain empty, got %d rows", legacyCommandRows)
	}
}

func TestMigrateV14ToV15RecoversDamagedMemoriesTableWithSourcePath(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v14-damaged-memories.db")

	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', '14')`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	assertSchemaVersion(t, s, schemaVersion)
	assertTableExists(t, s, "memories")
	assertColumnExists(t, s, "memories", "file_name")
	assertColumnExists(t, s, "memories", "source_path")

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	if err := s.InsertMemory(ctx, tx, "test-project", "team notes.v2.md", nil, 42, "# hi", "/tmp/team notes.v2.md"); err != nil {
		t.Fatalf("InsertMemory after migration recovery failed: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	entry, content, err := s.GetMemory(ctx, "test-project", "team notes.v2")
	if err != nil {
		t.Fatal(err)
	}
	if entry.FileName != "team notes.v2.md" {
		t.Fatalf("expected recovered file_name to round-trip, got %q", entry.FileName)
	}
	if content != "# hi" {
		t.Fatalf("expected recovered memory content to round-trip, got %q", content)
	}
}

func TestMigrateFixtureDeleteActionsAndCascadeCleanup(t *testing.T) {
	ctx := context.Background()
	path := copyMigrationFixture(t, "v13-delete-actions.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	assertSchemaVersion(t, s, schemaVersion)
	assertTableExists(t, s, "messages")
	assertTableExists(t, s, "tool_calls")
	assertTableExists(t, s, "commands")
	assertTableExists(t, s, "todos")
	assertTableExists(t, s, "ingest_issues")

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var messageCount int
	if err := s.db.GetContext(ctx, &messageCount, `SELECT COUNT(*) FROM messages`); err != nil {
		t.Fatal(err)
	}
	if messageCount != 0 {
		t.Fatalf("expected migrated ON DELETE CASCADE on messages.session_id, got %d rows", messageCount)
	}

	var toolCallCount int
	if err := s.db.GetContext(ctx, &toolCallCount, `SELECT COUNT(*) FROM tool_calls`); err != nil {
		t.Fatal(err)
	}
	if toolCallCount != 0 {
		t.Fatalf("expected migrated ON DELETE CASCADE on tool_calls.session_id, got %d rows", toolCallCount)
	}

	var commandCount int
	if err := s.db.GetContext(ctx, &commandCount, `SELECT COUNT(*) FROM commands`); err != nil {
		t.Fatal(err)
	}
	if commandCount != 0 {
		t.Fatalf("expected migrated ON DELETE CASCADE on commands.session_id, got %d rows", commandCount)
	}

	var sessionID *int64
	if err := s.db.GetContext(ctx, &sessionID, `SELECT session_id FROM todos WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if sessionID != nil {
		t.Fatalf("expected migrated ON DELETE SET NULL on todos.session_id, got %v", *sessionID)
	}

	tx, err = s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ingest_runs WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var ingestIssueCount int
	if err := s.db.GetContext(ctx, &ingestIssueCount, `SELECT COUNT(*) FROM ingest_issues`); err != nil {
		t.Fatal(err)
	}
	if ingestIssueCount != 0 {
		t.Fatalf("expected migrated ON DELETE CASCADE on ingest_issues.run_id, got %d rows", ingestIssueCount)
	}

	tx, err = s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM todos WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var todoItemCount int
	if err := s.db.GetContext(ctx, &todoItemCount, `SELECT COUNT(*) FROM todo_items WHERE todo_id = 1`); err != nil {
		t.Fatal(err)
	}
	if todoItemCount != 0 {
		t.Fatalf("expected migrated ON DELETE CASCADE on todo_items.todo_id, got %d rows", todoItemCount)
	}
}

func TestDeleteSessionCascade(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Set up: project, session, messages, commands, todo linked to session
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('p', 'P')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions (session_id, project_id, source_path) VALUES ('s1', 1, '/src/s1.jsonl')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages (session_id, seq, type, content) VALUES (1, 0, 'user', '"hello"')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO messages_fts (rowid, text_content) VALUES (1, 'hello')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO commands (session_id, seq, command, timestamp) VALUES (1, 0, 'ls', '2025-01-01')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tool_calls (session_id, seq, tool_name, tool_kind, input_json) VALUES (1, 0, 'Bash', 'shell', '{}')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO todos (file_name, session_id, item_count, statuses, source_path) VALUES ('t.json', 1, 1, '{}', '/src/t.json')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Delete the session cascade
	tx2, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSessionCascade(ctx, tx2, "/src/s1.jsonl"); err != nil {
		t.Fatalf("DeleteSessionCascade failed: %v", err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	var count int
	// Session should be gone
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM sessions`)
	if count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}
	// Messages should be gone
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM messages`)
	if count != 0 {
		t.Errorf("expected 0 messages, got %d", count)
	}
	// Commands should be gone
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM commands`)
	if count != 0 {
		t.Errorf("expected 0 commands, got %d", count)
	}
	// Tool calls should be gone
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM tool_calls`)
	if count != 0 {
		t.Errorf("expected 0 tool_calls, got %d", count)
	}
	// Todo should be unlinked (session_id = NULL) but still exist
	var sessID *int64
	s.db.GetContext(ctx, &sessID, `SELECT session_id FROM todos WHERE file_name = 't.json'`)
	if sessID != nil {
		t.Errorf("todo should have NULL session_id after cascade, got %v", *sessID)
	}
}

func TestDuplicateSessionAcrossProjects(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}

	pid1, err := s.InsertProject(ctx, tx, "proj-a", "Project A", "")
	if err != nil {
		t.Fatal(err)
	}
	pid2, err := s.InsertProject(ctx, tx, "proj-b", "Project B", "")
	if err != nil {
		t.Fatal(err)
	}

	sess := model.SessionEntry{SessionID: "shared-session"}

	id1, err := s.InsertSession(ctx, tx, pid1, sess, "/src/proj-a/shared-session.jsonl")
	if err != nil {
		t.Fatalf("insert into proj-a failed: %v", err)
	}
	id2, err := s.InsertSession(ctx, tx, pid2, sess, "/src/proj-b/shared-session.jsonl")
	if err != nil {
		t.Fatalf("insert into proj-b failed: %v", err)
	}

	if id1 == id2 {
		t.Error("sessions in different projects should have different db IDs")
	}

	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Verify both sessions exist
	var count int
	s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM sessions WHERE session_id = 'shared-session'`)
	if count != 2 {
		t.Errorf("expected 2 sessions with same session_id, got %d", count)
	}
}

func TestGetScanFinding(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Insert a finding
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO scan_findings (rule_id, description, source_type, source_id, match_redacted, scanned_at)
		 VALUES ('test-rule', 'Test finding', 'plan', 'test.md', 's3cr****', '2025-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	f, err := s.GetScanFinding(ctx, 1)
	if err != nil {
		t.Fatal("GetScanFinding failed:", err)
	}
	if f.RuleID != "test-rule" {
		t.Errorf("expected rule_id 'test-rule', got %q", f.RuleID)
	}
	if f.SourceType != "plan" {
		t.Errorf("expected source_type 'plan', got %q", f.SourceType)
	}

	// Non-existent ID should return error
	_, err = s.GetScanFinding(ctx, 999)
	if err == nil {
		t.Error("expected error for non-existent finding")
	}
}

func TestListProjectNames(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('b-proj', 'Beta')`)
	_, _ = tx.ExecContext(ctx, `INSERT INTO projects (dir_name, display_name) VALUES ('a-proj', 'Alpha')`)
	tx.Commit()

	names, err := s.ListProjectNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(names))
	}
	// Should be sorted by display_name
	if names[0].DisplayName != "Alpha" {
		t.Errorf("expected first project 'Alpha', got %q", names[0].DisplayName)
	}
}
