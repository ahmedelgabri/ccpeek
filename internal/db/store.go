// Package db is the ccpeek store: a session-centric SQLite database
// (pure-Go driver, ADR-0001) holding canonical records, usage/cost data,
// and user state.
//
// Discipline carried from docs/v2-plan.md §5.2 and §8.1: initialSchema is
// always the latest schema; migrations run only for existing older
// databases; opening a current database performs no scans, no backfills,
// no repairs. The store is an archive, not a cache — see schemaVersion
// for the migration policy (none pre-release, mandatory after v2.0).
// Derived data and user state are disjoint — ResetDerived (--rebuild)
// never touches user_annotations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store wraps the SQLite handles: one writer connection plus a reader
// pool, so queries stay responsive while ingest/scan transactions run
// (WAL readers never block on the writer).
type Store struct {
	db     *sql.DB
	readDB *sql.DB
	path   string // "" for in-memory
}

// Open opens (creating or migrating as needed) the database at path.
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
	// Single writer connection: SQLite has one writer anyway, and this
	// removes SQLITE_BUSY handling from every write site.
	sqlDB.SetMaxOpenConns(1)

	s := &Store{db: sqlDB, readDB: sqlDB, path: path}
	if err := s.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if path != ":memory:" {
		if err := s.ensureFilePermissions(); err != nil {
			sqlDB.Close()
			return nil, err
		}
		// A separate read pool — long ingest/rollup/scan transactions on
		// the writer must not queue API queries behind them (a watch-mode
		// re-index of a large session used to stall every request for its
		// whole duration). In-memory stores keep the single handle: a
		// second pool would open a different database.
		readDB, err := sql.Open("sqlite", dsn+"&_pragma=query_only(1)")
		if err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("opening read pool: %w", err)
		}
		readDB.SetMaxOpenConns(4)
		s.readDB = readDB
	}
	return s, nil
}

// Close closes the underlying handles.
func (s *Store) Close() error {
	var err error
	if s.readDB != s.db {
		err = s.readDB.Close()
	}
	if cerr := s.db.Close(); cerr != nil {
		err = cerr
	}
	return err
}

// DB exposes the writer handle to sibling packages (ingest, query).
func (s *Store) DB() *sql.DB { return s.db }

// ReadDB exposes the reader pool: use it for every pure read so queries
// never wait behind write transactions.
func (s *Store) ReadDB() *sql.DB { return s.readDB }

// GetMeta reads a meta value; ok=false when the key is absent.
func (s *Store) GetMeta(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// GetMetaMulti reads several meta values in ONE round trip, off the read
// pool. Absent keys are simply missing from the result.
//
// GetMeta goes through s.db — the single writer connection. The health and
// readiness endpoints must not: the SPA polls health every 1.5s for the
// whole duration of the first index pass, which is exactly when that one
// connection is saturated by per-source ingest transactions. Three
// separate GetMeta calls per poll therefore queued behind ingest writes
// and fed their own contention back into the pass.
func (s *Store) GetMetaMulti(ctx context.Context, keys ...string) (map[string]string, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = k
	}
	rows, err := s.ReadDB().QueryContext(ctx,
		`SELECT key, value FROM meta WHERE key IN (?`+
			strings.Repeat(",?", len(keys)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string, len(keys))
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SetMeta writes a meta value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ErrFutureSchema means the database was created by a newer ccpeek.
var ErrFutureSchema = errors.New("database schema is newer than this ccpeek version")

// ErrNoMigrationPath means the database is older than this build supports
// upgrading from (a pre-release database with no compatibility promise).
var ErrNoMigrationPath = errors.New("no migration path from this database version")

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

	case current < baseVersion:
		return fmt.Errorf("%w: database at v%d, this build upgrades from v%d — pre-release databases must be re-created (delete %s and its -wal/-shm files)",
			ErrNoMigrationPath, current, baseVersion, s.path)

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

func (s *Store) applyMigration(ctx context.Context, from int) error {
	idx := from - baseVersion
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

// createAll builds the whole schema in ONE transaction, search index
// included — FTS5 virtual tables and their triggers are transactional in
// SQLite, so there is no reason to leave either phase exposed.
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
	if _, err := tx.ExecContext(ctx, derivedVirtualSchema); err != nil {
		return fmt.Errorf("creating search index: %w", err)
	}
	return tx.Commit()
}

// ResetDerived drops and recreates everything rebuildable from sources.
// User state (user_annotations) survives — this is what --rebuild calls.
//
// Every drop and every recreate lives in a SINGLE transaction. Dropping
// the search index outside it meant a failure (or a kill) between the two
// phases left a database with all derived tables intact and no
// search_docs/search_fts — a state nothing detects, because Open performs
// no repairs by design and these tables are only ever created here.
func (s *Store) ResetDerived(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return err
	}
	// Triggers first: they reference both tables of the search pair.
	for _, trigger := range []string{"search_docs_ai", "search_docs_ad", "search_docs_au"} {
		if _, err := tx.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+trigger); err != nil {
			return fmt.Errorf("dropping %s: %w", trigger, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS search_fts`); err != nil {
		return fmt.Errorf("dropping search index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS search_docs`); err != nil {
		return fmt.Errorf("dropping search docs: %w", err)
	}
	for _, table := range derivedTables {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+table); err != nil {
			return fmt.Errorf("dropping %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx, derivedSchema); err != nil {
		return fmt.Errorf("recreating derived schema: %w", err)
	}
	if _, err := tx.ExecContext(ctx, derivedVirtualSchema); err != nil {
		return fmt.Errorf("recreating search index: %w", err)
	}
	return tx.Commit()
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
	return s.SetMeta(ctx, "schema_version", strconv.Itoa(v))
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
