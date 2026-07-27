package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// TestReadStoreStateReportsMetaVersion: the store versions itself in
// meta['schema_version'], never SQLite's user_version pragma — doctor
// must report that key (the earlier pragma read printed 0 against
// every real store) — and diagnosing must leave the database bytes
// untouched.
func TestReadStoreStateReportsMetaVersion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v2.db")
	store, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMeta(ctx, metaV1ImportState, "no-legacy-db"); err != nil {
		t.Fatal(err)
	}
	// The store's own idea of its version, read back through its API so
	// the test survives future schema bumps.
	want, ok, err := store.GetMeta(ctx, "schema_version")
	if err != nil || !ok || want == "0" || want == "" {
		t.Fatalf("store meta schema_version = %q (ok=%v err=%v)", want, ok, err)
	}
	store.Close()

	hash := func() string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}
	before := hash()

	st, err := readStoreState(path)
	if err != nil {
		t.Fatalf("readStoreState: %v", err)
	}
	if st.SchemaVersion != want {
		t.Errorf("doctor schema version = %q, want %q (meta, not pragma)", st.SchemaVersion, want)
	}
	if st.V1ImportState != "no-legacy-db" {
		t.Errorf("v1 import state = %q, want no-legacy-db", st.V1ImportState)
	}
	if st.MigratedAt == "" {
		t.Error("migrated_at rendered empty, want a value or an explicit (unset)")
	}
	if after := hash(); after != before {
		t.Error("diagnosis modified the database file — doctor must be read-only")
	}
}

// TestReadStoreStateToleratesForeignFile: a SQLite file without the
// meta table diagnoses with an explicit note instead of an error.
func TestReadStoreStateToleratesForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "foreign.db")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := readStoreState(path)
	if err != nil {
		t.Fatalf("readStoreState on empty file: %v", err)
	}
	if st.SchemaVersion == "" || st.SchemaVersion[0] != '(' {
		t.Errorf("schema version = %q, want an explicit unreadable note", st.SchemaVersion)
	}
}
