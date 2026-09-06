package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ahmedelgabri/ccpeek/internal/sqliteutil"
)

// Backup produces a consistent, standalone SQLite snapshot, including committed
// WAL data. It never overwrites an existing destination and publishes only a
// verified, synced file. No source files or personal agent roots are consulted.
func (s *Store) Backup(ctx context.Context, destination string) error {
	ctx, unlock, err := s.LockMaintenance(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return snapshot(ctx, s.db, destination)
}

// BackupFile snapshots an archive without first migrating it. The CLI uses
// this path so an older database can be backed up before its first new-version open.
func BackupFile(ctx context.Context, source, destination string) error {
	ctx, unlock, err := lockPath(ctx, source)
	if err != nil {
		return err
	}
	defer unlock()
	db, err := sql.Open("sqlite", sqliteutil.URI(source, "mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := verifyBackup(ctx, db); err != nil {
		return err
	}
	return snapshot(ctx, db, destination)
}

// Restore writes into a NEW archive path. Replacing a database that a running
// server has open is unsafe, so overwrite is deliberately not supported.
func Restore(ctx context.Context, source, destination string) error {
	db, err := sql.Open("sqlite", sqliteutil.URI(source, "mode=ro&_pragma=busy_timeout(5000)"))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := verifyBackup(ctx, db); err != nil {
		return fmt.Errorf("invalid backup: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	ctx, unlock, err := lockPath(ctx, destination)
	if err != nil {
		return err
	}
	defer unlock()
	return snapshot(ctx, db, destination)
}

func snapshot(ctx context.Context, source *sql.DB, destination string) error {
	if destination == "" || destination == ":memory:" {
		return fmt.Errorf("backup destination must be a file")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Lstat(destination + suffix); err == nil {
			return fmt.Errorf("destination already exists: %s", destination+suffix)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".ccpeek-backup-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := source.ExecContext(ctx, `VACUUM INTO ?`, temporary); err != nil {
		return fmt.Errorf("creating SQLite snapshot: %w", err)
	}
	check, err := sql.Open("sqlite", sqliteutil.URI(temporary, "mode=ro"))
	if err != nil {
		return err
	}
	err = verifyBackup(ctx, check)
	closeErr := check.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	file, err = os.OpenFile(temporary, os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	err = file.Sync()
	closeErr = file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Link is an atomic no-replace publication on the same filesystem.
	if err := os.Link(temporary, destination); err != nil {
		return fmt.Errorf("publishing backup: %w", err)
	}
	return nil
}

func verifyBackup(ctx context.Context, db *sql.DB) error {
	var integrity string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return fmt.Errorf("integrity check: %s", integrity)
	}
	rows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	bad := rows.Next()
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if bad {
		return fmt.Errorf("foreign key check failed")
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
		return err
	}
	if version < baseVersion || version > schemaVersion {
		return fmt.Errorf("unsupported archive schema %d", version)
	}
	var tables int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('agents','sessions','messages','user_annotations')`).Scan(&tables); err != nil {
		return err
	}
	if tables != 4 {
		return fmt.Errorf("not a ccpeek archive")
	}
	return nil
}
