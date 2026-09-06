package db

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenCurrentArchiveDuringWrite(t *testing.T) {
	store, path := openTemp(t)
	ctx := context.Background()
	if err := store.SetMeta(ctx, "startup-test", "committed"); err != nil {
		t.Fatal(err)
	}
	_, unlock, err := store.LockMaintenance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE meta SET value = 'uncommitted' WHERE key = 'startup-test'`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	meta, err := other.GetMetaMulti(ctx, "startup-test")
	if err != nil || meta["startup-test"] != "committed" {
		t.Fatalf("read while another process writes: %v, %v", meta, err)
	}
}

func TestOpenMigrationStillWaitsForMaintenance(t *testing.T) {
	store, path := openTemp(t)
	ctx := context.Background()
	// Simulate an older schema. The canceled opener must never run its migration.
	if err := store.writeVersion(ctx, schemaVersion-1); err != nil {
		t.Fatal(err)
	}
	_, unlock, err := store.LockMaintenance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	other, err := Open(ctx, path)
	if other != nil {
		other.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("migration did not wait for maintenance: %v", err)
	}
}

func TestConcurrentArchiveInitialization(t *testing.T) {
	for _, mode := range []string{"fresh", "migration"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "archive.db")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if mode == "migration" {
				store, err := Open(ctx, path)
				if err != nil {
					t.Fatal(err)
				}
				// Migration 17 must seed this session exactly once. Reusing a
				// version read before locking would hit a duplicate primary key.
				_, err = store.DB().ExecContext(ctx, `
					DROP TABLE dirty_sessions;
					INSERT INTO agents(id, slug) VALUES (1, 'test');
					INSERT INTO sessions(agent_id, external_id) VALUES (1, 'retained');
					UPDATE meta SET value = '16' WHERE key = 'schema_version';`)
				store.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() {
					store, err := Open(ctx, path)
					if err != nil {
						t.Error(err)
						return
					}
					defer store.Close()
					version, err := store.readVersion(ctx)
					if err != nil || version != schemaVersion {
						t.Errorf("version=%d err=%v", version, err)
					}
					if mode == "migration" {
						var dirty int
						err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM dirty_sessions`).Scan(&dirty)
						if err != nil || dirty != 1 {
							t.Errorf("dirty sessions=%d err=%v", dirty, err)
						}
					}
				})
			}
			workers.Wait()
		})
	}
}
