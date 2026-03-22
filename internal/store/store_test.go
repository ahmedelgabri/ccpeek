package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

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

func TestMigrateBackfillsProjectCanonicalPath(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ccpeek.db")

	db, err := sqlx.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '11')`,
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			dir_name TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL
		)`,
		`CREATE TABLE sessions (
			id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			project_id INTEGER NOT NULL,
			first_prompt TEXT NOT NULL DEFAULT '',
			message_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			modified_at TEXT NOT NULL DEFAULT '',
			git_branch TEXT NOT NULL DEFAULT '',
			project_path TEXT NOT NULL DEFAULT '',
			todo_file_name TEXT NOT NULL DEFAULT '',
			has_file_history INTEGER NOT NULL DEFAULT 0,
			bash_command_count INTEGER NOT NULL DEFAULT 0,
			tool_use_counts TEXT NOT NULL DEFAULT '{}',
			estimated_tokens INTEGER NOT NULL DEFAULT 0,
			source_path TEXT NOT NULL DEFAULT '',
			UNIQUE(session_id, project_id)
		)`,
		`INSERT INTO projects (id, dir_name, display_name) VALUES (1, '-Users-me-my-project', '-Users-me-my-project')`,
		`INSERT INTO sessions (session_id, project_id, modified_at, project_path, source_path)
		 VALUES ('sess-1', 1, '2024-01-02T00:00:00Z', '/Users/me/my-project', '/src/sess-1.jsonl')`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			t.Fatalf("seed old schema: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	project, err := s.GetProject(ctx, "-Users-me-my-project")
	if err != nil {
		t.Fatal(err)
	}
	if project.CanonicalPath != "/Users/me/my-project" {
		t.Fatalf("expected canonical path to be backfilled, got %q", project.CanonicalPath)
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

func TestMigrateDeleteActionsAndCascadeCleanup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ccpeek-migrate-fk.db")

	db, err := sqlx.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}

	stmts := []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO meta (key, value) VALUES ('schema_version', '13')`,
		`CREATE TABLE projects (
			id INTEGER PRIMARY KEY,
			dir_name TEXT NOT NULL UNIQUE,
			display_name TEXT NOT NULL,
			canonical_path TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE sessions (
			id INTEGER PRIMARY KEY,
			session_id TEXT NOT NULL,
			project_id INTEGER NOT NULL REFERENCES projects(id),
			first_prompt TEXT NOT NULL DEFAULT '',
			message_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			modified_at TEXT NOT NULL DEFAULT '',
			git_branch TEXT NOT NULL DEFAULT '',
			project_path TEXT NOT NULL DEFAULT '',
			todo_file_name TEXT NOT NULL DEFAULT '',
			has_file_history INTEGER NOT NULL DEFAULT 0,
			bash_command_count INTEGER NOT NULL DEFAULT 0,
			tool_use_counts TEXT NOT NULL DEFAULT '{}',
			estimated_tokens INTEGER NOT NULL DEFAULT 0,
			source_path TEXT NOT NULL DEFAULT '',
			UNIQUE(session_id, project_id)
		)`,
		`CREATE TABLE todos (
			id INTEGER PRIMARY KEY,
			file_name TEXT NOT NULL UNIQUE,
			session_id INTEGER REFERENCES sessions(id),
			item_count INTEGER NOT NULL DEFAULT 0,
			statuses TEXT NOT NULL DEFAULT '{}',
			source_path TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE todo_items (
			id INTEGER PRIMARY KEY,
			todo_id INTEGER NOT NULL REFERENCES todos(id),
			seq INTEGER NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			active_form TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO projects (id, dir_name, display_name) VALUES (1, 'proj', 'Project')`,
		`INSERT INTO sessions (id, session_id, project_id, source_path) VALUES (1, 'sess-1', 1, '/src/sess-1.jsonl')`,
		`INSERT INTO todos (id, file_name, session_id, item_count, statuses, source_path) VALUES (1, 'todo.json', 1, 1, '{}', '/src/todo.json')`,
		`INSERT INTO todo_items (id, todo_id, seq, content, status, active_form) VALUES (1, 1, 0, 'fix bug', 'pending', 'Fix bug')`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			db.Close()
			t.Fatalf("seed v13 schema: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

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
	if _, err := tx.ExecContext(ctx, `DELETE FROM todos WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM todo_items WHERE todo_id = 1`); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected migrated ON DELETE CASCADE on todo_items.todo_id, got %d rows", count)
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
