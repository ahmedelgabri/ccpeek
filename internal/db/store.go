// Package db is the ccpeek v2 store: a session-centric SQLite database
// (pure-Go driver, ADR-0001) holding canonical records, usage/cost data,
// and user state.
//
// Discipline carried from docs/v2-plan.md §5.2 and §8.1: initialSchema is
// always the latest schema; migrations run only for existing older
// databases; opening a current database performs no scans, no backfills,
// no repairs. Derived data and user state are disjoint — ResetDerived
// (--rebuild) never touches user_annotations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrFutureSchema means the database was created by a newer ccpeek.
var ErrFutureSchema = errors.New("database schema is newer than this ccpeek version")

// Store wraps the SQLite handle.
type Store struct {
	db   *sql.DB
	path string // "" for in-memory
}

// Open opens (creating or migrating as needed) the v2 database at path.
// Pass ":memory:" for an in-memory store (tests).
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	} else if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	// Single connection: SQLite has one writer anyway, and this removes
	// SQLITE_BUSY handling from every call site.
	sqlDB.SetMaxOpenConns(1)

	s := &Store{db: sqlDB, path: path}
	if err := s.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := s.ensureFilePermissions(); err != nil {
			sqlDB.Close()
			return nil, err
		}
	}
	return s, nil
}

// Close closes the underlying handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the handle to sibling v2 packages (ingest, query).
func (s *Store) DB() *sql.DB { return s.db }

// SchemaVersion reports the stored schema version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return s.readVersion(ctx)
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("creating meta table: %w", err)
	}
	current, err := s.readVersion(ctx)
	if err != nil {
		return err
	}

	switch {
	case current == 0:
		// Fresh database: create the latest schema directly.
		if err := s.createAll(ctx); err != nil {
			return err
		}
		return s.writeVersion(ctx, schemaVersion)

	case current > schemaVersion:
		return fmt.Errorf("%w: database at v%d, binary supports v%d",
			ErrFutureSchema, current, schemaVersion)

	case current < schemaVersion:
		for v := current; v < schemaVersion; v++ {
			if err := s.applyMigration(ctx, v); err != nil {
				return fmt.Errorf("migrating schema v%d→v%d: %w", v, v+1, err)
			}
		}
		return nil

	default:
		// Current version: nothing to do — by design, no backfills here.
		return nil
	}
}

func (s *Store) createAll(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, derivedSchema); err != nil {
		return fmt.Errorf("creating derived schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, userSchema); err != nil {
		return fmt.Errorf("creating user schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, derivedVirtualSchema); err != nil {
		return fmt.Errorf("creating search index: %w", err)
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, from int) error {
	idx := from - 1
	if idx < 0 || idx >= len(migrations) {
		return fmt.Errorf("no migration registered from v%d", from)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := migrations[idx](ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		strconv.Itoa(from+1)); err != nil {
		return err
	}
	return tx.Commit()
}

// ResetDerived drops and recreates everything rebuildable from sources.
// User state (user_annotations) survives — this is what --rebuild calls.
func (s *Store) ResetDerived(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DROP TABLE IF EXISTS search_fts`); err != nil {
		return fmt.Errorf("dropping search index: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	for _, table := range derivedTables {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return fmt.Errorf("dropping %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, derivedSchema); err != nil {
		return fmt.Errorf("recreating derived schema: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, derivedVirtualSchema); err != nil {
		return fmt.Errorf("recreating search index: %w", err)
	}
	return nil
}

func (s *Store) readVersion(ctx context.Context) (int, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading schema version: %w", err)
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("corrupt schema version %q: %w", raw, err)
	}
	return v, nil
}

func (s *Store) writeVersion(ctx context.Context, v int) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES ('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		strconv.Itoa(v))
	return err
}

// ensureFilePermissions tightens the database files to owner-only, matching
// v1 behavior — transcripts routinely contain secrets.
func (s *Store) ensureFilePermissions() error {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := s.path + suffix
		if err := os.Chmod(p, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("tightening permissions on %s: %w", p, err)
		}
	}
	return nil
}
