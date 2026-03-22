package store

import (
	"context"
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
	ctx := context.Background()
	path := copyMigrationFixture(t, "v10-derived-data.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	assertSchemaVersion(t, s, schemaVersion)

	project, err := s.GetProject(ctx, "-Users-me-my-project")
	if err != nil {
		t.Fatal(err)
	}
	if project.CanonicalPath != "/Users/me/my-project" {
		t.Fatalf("expected canonical path to be backfilled, got %q", project.CanonicalPath)
	}

	var toolCallCount int
	if err := s.db.GetContext(ctx, &toolCallCount, `SELECT COUNT(*) FROM tool_calls`); err != nil {
		t.Fatal(err)
	}
	if toolCallCount != 1 {
		t.Fatalf("expected 1 tool call after fixture migration, got %d", toolCallCount)
	}

	var resultText string
	if err := s.db.GetContext(ctx, &resultText, `SELECT result_text FROM tool_calls LIMIT 1`); err != nil {
		t.Fatal(err)
	}
	if resultText != "ok" {
		t.Fatalf("expected backfilled tool result text %q, got %q", "ok", resultText)
	}

	commands, total, err := s.ListCommands(ctx, 10, 0, CommandFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("expected 1 command after fixture migration, got %d", total)
	}
	if len(commands) != 1 || commands[0].Command != "echo hi" {
		t.Fatalf("unexpected migrated commands: %+v", commands)
	}

	var conversationDocs int
	if err := s.db.GetContext(ctx, &conversationDocs, `SELECT COUNT(*) FROM search_documents_fts WHERE group_type = ?`, searchGroupConversations); err != nil {
		t.Fatal(err)
	}
	if conversationDocs < 1 {
		t.Fatalf("expected conversation search documents after fixture migration, got %d", conversationDocs)
	}

	var commandDocs int
	if err := s.db.GetContext(ctx, &commandDocs, `SELECT COUNT(*) FROM search_documents_fts WHERE group_type = ?`, searchGroupCommands); err != nil {
		t.Fatal(err)
	}
	if commandDocs != 1 {
		t.Fatalf("expected 1 command search document after fixture migration, got %d", commandDocs)
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

func TestMigrateFixtureDeleteActionsAndCascadeCleanup(t *testing.T) {
	ctx := context.Background()
	path := copyMigrationFixture(t, "v13-delete-actions.db")

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	assertSchemaVersion(t, s, schemaVersion)

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
