package store

import (
	"fmt"
	"strconv"

	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

// Store wraps a SQLite database for indexed data.
type Store struct {
	db *sqlx.DB
}

// Open opens (or creates) a SQLite database at the given path.
// Use ":memory:" for an in-memory database.
func Open(dsn string) (*Store, error) {
	if dsn != ":memory:" {
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

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating schema: %w", err)
	}

	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Reset drops all data and recreates the schema. Used for full rebuild.
func (s *Store) Reset() error {
	tables := []string{
		"messages_fts", "messages", "todo_items", "todos",
		"file_versions", "file_history",
		"task_items", "task_groups", "usage_facets", "usage_report", "paste_cache",
		"memories", "sessions", "projects",
		"plans", "shell_snapshots", "history", "scan_findings", "source_files", "meta",
	}
	for _, t := range tables {
		if _, err := s.db.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return fmt.Errorf("dropping table %s: %w", t, err)
		}
	}
	return s.migrate()
}

// migrate applies the initial schema (if needed) then runs any pending
// sequential migrations to bring the database up to schemaVersion.
func (s *Store) migrate() error {
	// Ensure meta table exists so we can read the version
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("creating meta table: %w", err)
	}

	var currentVersion int
	row := s.db.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'")
	if err := row.Scan(&currentVersion); err != nil {
		// No version yet — fresh database, apply initial schema
		currentVersion = 0
	}

	if currentVersion >= schemaVersion {
		return nil
	}

	// Apply initial schema (all CREATE IF NOT EXISTS, safe to re-run)
	if _, err := s.db.Exec(initialSchema); err != nil {
		return fmt.Errorf("applying initial schema: %w", err)
	}

	// If this was a fresh database (no version set), we're now at baseline v4
	if currentVersion == 0 {
		currentVersion = 4
	}

	// Apply sequential migrations from currentVersion to schemaVersion
	// migrations[0] = v4→v5, migrations[1] = v5→v6, etc.
	baseVersion := 4 // the schema version that initialSchema represents
	for v := currentVersion; v < schemaVersion; v++ {
		idx := v - baseVersion
		if idx < 0 || idx >= len(migrations) {
			continue
		}

		tx, err := s.db.Beginx()
		if err != nil {
			return fmt.Errorf("beginning migration %d: %w", v+1, err)
		}

		if err := migrations[idx](tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d→v%d: %w", v, v+1, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration v%d→v%d: %w", v, v+1, err)
		}
	}

	// Record final version
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', ?)`,
		strconv.Itoa(schemaVersion),
	)
	return err
}
