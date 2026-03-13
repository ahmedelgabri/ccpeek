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

// Reset drops all data and recreates the schema. Used for full re-index.
func (s *Store) Reset() error {
	tables := []string{
		"messages_fts", "messages", "todo_items", "todos",
		"file_versions", "file_history",
		"task_items", "task_groups", "usage_facets", "usage_report", "paste_cache",
		"memories", "sessions", "projects",
		"plans", "shell_snapshots", "history", "source_files", "meta",
	}
	for _, t := range tables {
		if _, err := s.db.Exec("DROP TABLE IF EXISTS " + t); err != nil {
			return fmt.Errorf("dropping table %s: %w", t, err)
		}
	}
	return s.migrate()
}

func (s *Store) migrate() error {
	// Check current schema version
	var currentVersion int
	row := s.db.QueryRow("SELECT value FROM meta WHERE key = 'schema_version'")
	if err := row.Scan(&currentVersion); err == nil {
		if currentVersion >= schemaVersion {
			return nil
		}
	}

	// Apply schema
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}

	// Set version
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO meta (key, value) VALUES ('schema_version', ?)`,
		strconv.Itoa(schemaVersion),
	)
	return err
}

// DB returns the underlying sqlx.DB for advanced queries.
func (s *Store) DB() *sqlx.DB {
	return s.db
}
