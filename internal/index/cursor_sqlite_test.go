package index

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func createCursorSQLiteLayout(t *testing.T, appDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(appDir, "User", "workspaceStorage"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeGlobalComposerFixtureDB(t *testing.T, dbPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS cursorDiskKV (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	payload := `{"composerId":"comp-1","name":"Fixture Composer","createdAt":1700000000,"lastUpdatedAt":1700000100,"modelConfig":{"modelName":"gpt-4.1"}}`
	if _, err := db.Exec(`INSERT INTO cursorDiskKV (key, value) VALUES (?, ?)`, "composerData:comp-1", payload); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cursorDiskKV (key, value) VALUES (?, ?)`, "bubbleId:comp-1:1", `{"text":"hello"}`); err != nil {
		t.Fatal(err)
	}
}

func TestFindCursorAppDirResolutionOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	explicit := filepath.Join(t.TempDir(), "explicit")
	createCursorSQLiteLayout(t, explicit)
	if got := findCursorAppDir(explicit); got != explicit {
		t.Fatalf("expected explicit layout %q, got %q", explicit, got)
	}

	outer := filepath.Join(t.TempDir(), "outer")
	nested := filepath.Join(outer, "Cursor")
	createCursorSQLiteLayout(t, nested)
	if got := findCursorAppDir(outer); got != nested {
		t.Fatalf("expected nested layout %q, got %q", nested, got)
	}

	dotCursor := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(dotCursor, 0o755); err != nil {
		t.Fatal(err)
	}
	defaultDir := defaultCursorAppDir()
	createCursorSQLiteLayout(t, defaultDir)
	if !shouldFallbackToDefaultCursorAppDir(dotCursor) {
		t.Fatalf("expected fallback to default app dir for %q", dotCursor)
	}
	if got := findCursorAppDir(dotCursor); got != defaultDir {
		t.Fatalf("expected fallback layout %q, got %q", defaultDir, got)
	}
}

func TestIndexCursorSQLiteIndexesMetadataOnlySession(t *testing.T) {
	ctx := context.Background()
	cursorDir := t.TempDir()
	globalDB := filepath.Join(cursorDir, "User", "globalStorage", "state.vscdb")
	writeGlobalComposerFixtureDB(t, globalDB)

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultRunOptions
	projectCount, sessionCount, err := indexCursorSQLite(ctx, cursorDir, s, tx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if projectCount != 1 || sessionCount != 1 {
		t.Fatalf("expected one sqlite project/session, got projects=%d sessions=%d", projectCount, sessionCount)
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected one project in store, got %d", len(projects))
	}
	if projects[0].Source != model.SourceCursor {
		t.Fatalf("expected cursor source project, got %q", projects[0].Source)
	}
	if len(projects[0].Sessions) != 1 {
		t.Fatalf("expected one session in project, got %d", len(projects[0].Sessions))
	}
	sess := projects[0].Sessions[0]
	if !sess.MetadataOnly {
		t.Fatal("expected sqlite-derived session to be metadata-only")
	}
	if sess.Source != model.SourceCursor {
		t.Fatalf("expected cursor session source, got %q", sess.Source)
	}
	if sess.ModelName != "gpt-4.1" {
		t.Fatalf("expected model name from sqlite payload, got %q", sess.ModelName)
	}
}

func TestIndexCursorSQLiteRespectsSizeLimit(t *testing.T) {
	ctx := context.Background()
	cursorDir := t.TempDir()
	globalDB := filepath.Join(cursorDir, "User", "globalStorage", "state.vscdb")
	writeGlobalComposerFixtureDB(t, globalDB)

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultRunOptions
	opts.MaxCursorSQLiteBytes = 1
	projectCount, sessionCount, err := indexCursorSQLite(ctx, cursorDir, s, tx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if projectCount != 0 || sessionCount != 0 {
		t.Fatalf("expected sqlite indexing skipped by size limit, got projects=%d sessions=%d", projectCount, sessionCount)
	}
}
