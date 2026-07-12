package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

// Store wraps a SQLite database for indexed data.
type Store struct {
	db   *sqlx.DB
	path string
}

// Open opens (or creates) a SQLite database at the given path.
// Use ":memory:" for an in-memory database.
func Open(ctx context.Context, dsn string) (*Store, error) {
	dbPath := ""
	if dsn != ":memory:" {
		dbPath = dsn
		dsn += "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"
	} else {
		dsn = ":memory:?_foreign_keys=on"
	}

	db, err := sqlx.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Single connection for in-memory databases (they're per-connection).
	// For file-based, allow some concurrency.
	if dsn == ":memory:?_foreign_keys=on" {
		db.SetMaxOpenConns(1)
	}

	s := &Store{db: db, path: dbPath}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating schema: %w", err)
	}
	if err := s.EnsureFilePermissions(); err != nil {
		db.Close()
		return nil, fmt.Errorf("tightening database permissions: %w", err)
	}

	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// EnsureFilePermissions limits database files to owner-only access.
func (s *Store) EnsureFilePermissions() error {
	if s.path == "" {
		return nil
	}

	for _, path := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", filepath.Base(path), err)
		}
		if info.Mode().Perm() == 0o600 {
			continue
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// Reset drops all data and recreates the schema. Used for full rebuild.
func (s *Store) Reset(ctx context.Context) error {
	tables := []string{
		"messages_fts", "search_documents_fts", "tool_calls", "commands", "messages", "todo_items", "todos",
		"file_versions", "file_history",
		"task_items", "task_groups", "usage_facets", "usage_report", "paste_cache",
		"memories", "sessions", "projects",
		"plans", "shell_snapshots", "history", "scan_findings",
		"ingest_issues", "ingest_runs", "source_files", "meta",
	}
	for _, t := range tables {
		if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS "+t); err != nil {
			return fmt.Errorf("dropping table %s: %w", t, err)
		}
	}
	return s.migrate(ctx)
}

// migrate applies the initial schema (if needed) then runs any pending
// sequential migrations to bring the database up to schemaVersion.
func (s *Store) migrate(ctx context.Context) error {
	// Ensure meta table exists so we can read the version
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("creating meta table: %w", err)
	}

	var currentVersion int
	row := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'schema_version'")
	if err := row.Scan(&currentVersion); err != nil {
		// No version yet — fresh database, apply initial schema
		currentVersion = 0
	}

	// Always re-run initial schema (all CREATE IF NOT EXISTS) to
	// recover from corrupt databases with missing tables.
	if _, err := s.db.ExecContext(ctx, initialSchema); err != nil {
		return fmt.Errorf("applying initial schema: %w", err)
	}

	if currentVersion >= schemaVersion {
		if err := s.backfillToolCalls(ctx); err != nil {
			return err
		}
		return s.backfillSearchIndex(ctx)
	}

	// If this was a fresh database (no version set), we're now at baseline v4
	if currentVersion == 0 {
		currentVersion = 4
	}

	// Disable foreign keys so table-recreation migrations (DROP + RENAME)
	// don't fail. Must be done outside a transaction.
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return fmt.Errorf("disabling foreign keys: %w", err)
	}

	// Apply sequential migrations from currentVersion to schemaVersion
	// migrations[0] = v4→v5, migrations[1] = v5→v6, etc.
	baseVersion := 4 // the schema version that initialSchema represents
	for v := currentVersion; v < schemaVersion; v++ {
		idx := v - baseVersion
		if idx < 0 || idx >= len(migrations) {
			continue
		}

		tx, err := s.db.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning migration %d: %w", v+1, err)
		}

		if err := migrations[idx](ctx, tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d→v%d: %w", v, v+1, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration v%d→v%d: %w", v, v+1, err)
		}
	}

	// Re-enable foreign keys and verify no violations were introduced
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("re-enabling foreign keys: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("checking foreign keys: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, rowid, parent string
		var fkid int
		rows.Scan(&table, &rowid, &parent, &fkid)
		return fmt.Errorf("foreign key violation after migration: table %s row %s references %s", table, rowid, parent)
	}

	// Record final version
	_, err = s.db.ExecContext(
		ctx,
		`INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', ?)`,
		strconv.Itoa(schemaVersion),
	)
	if err != nil {
		return err
	}

	if err := s.backfillToolCalls(ctx); err != nil {
		return fmt.Errorf("backfilling tool calls: %w", err)
	}
	if err := s.backfillSearchIndex(ctx); err != nil {
		return fmt.Errorf("backfilling search index: %w", err)
	}

	return nil
}
