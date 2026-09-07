package cmd

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
)

func TestNoIndexCompletedImportDoesNotWaitForMaintenance(t *testing.T) {
	for _, state := range []string{v1ImportSuccess, v1ImportNoLegacyDB} {
		t.Run(state, func(t *testing.T) {
			legacy := filepath.Join(t.TempDir(), "ccpeek.db")
			cmd := pinRoots(t, legacy, t.TempDir())
			store, err := db.Open(context.Background(), storeDBPath(legacy))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			for key, value := range map[string]string{"migrated_at": "test", metaV1ImportState: state} {
				if err := store.SetMeta(context.Background(), key, value); err != nil {
					t.Fatal(err)
				}
			}
			_, unlock, err := store.LockMaintenance(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			tx, err := store.DB().BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES ('uncommitted','1')`); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			// Test both the busy Store's read pool and a new --no-index engine.
			maybeImportV1(ctx, store, legacy, io.Discard)
			if err := ctx.Err(); err != nil {
				t.Fatalf("completed import waited: %v", err)
			}
			eng, err := openEngine(ctx, cmd, indexSkip{skip: true, flag: "--no-index"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			if err := ctx.Err(); err != nil {
				t.Fatalf("no-index engine waited: %v", err)
			}
		})
	}
}
