package index

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func writeGlobalComposerFileHistoryFixtureDB(t *testing.T, dbPath string) {
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
	payload := `{
		"composerId":"comp-1",
		"lastUpdatedAt":1710000000,
		"conversation":[
			{
				"codeBlocks":[{"file_path":"src/main.go","content":"package main\nfunc main() {}"}],
				"diffHistories":[{"filePath":"src/main.go","diff":"@@ -1 +1 @@\n-package x\n+package main"}]
			}
		]
	}`
	if _, err := db.Exec(`INSERT INTO cursorDiskKV (key, value) VALUES (?, ?)`, "composerData:comp-1", payload); err != nil {
		t.Fatal(err)
	}
}

func TestExtractGlobalComposerFileHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.vscdb")
	writeGlobalComposerFileHistoryFixtureDB(t, dbPath)

	sessions, err := extractGlobalComposerFileHistory(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected one file-history session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.ConversationID != "comp-1" {
		t.Fatalf("expected composer conversation id comp-1, got %q", s.ConversationID)
	}
	if len(s.Files) == 0 {
		t.Fatal("expected extracted file versions from sqlite payload")
	}
	found := false
	for _, f := range s.Files {
		if f.FilePath == "src/main.go" {
			found = true
			if f.ChangeKind == "" {
				t.Fatal("expected change kind for extracted file version")
			}
		}
	}
	if !found {
		t.Fatalf("expected file version for src/main.go, got %+v", s.Files)
	}
}

func TestExtractSQLiteBubbleChangesNestedCollections(t *testing.T) {
	payload := map[string]any{
		"timestamp": "2026-01-01T10:00:00Z",
		"suggestedCodeBlocks": []any{
			map[string]any{
				"path":    "a/b/c.txt",
				"content": "hello",
			},
		},
		"context": map[string]any{
			"selections": []any{
				map[string]any{
					"relativeWorkspacePath": "src/selection.go",
					"text":                  "selected content",
				},
			},
		},
	}

	changes := extractSQLiteBubbleChanges(payload, "")
	if len(changes) < 2 {
		t.Fatalf("expected nested extraction to produce multiple changes, got %d", len(changes))
	}

	paths := make(map[string]bool)
	for _, c := range changes {
		paths[c.FilePath] = true
	}
	if !paths["a/b/c.txt"] || !paths["src/selection.go"] {
		t.Fatalf("expected extracted paths not found, got %+v", paths)
	}
}

func TestIndexCursorSQLiteFileHistoryRespectsSizeLimit(t *testing.T) {
	ctx := context.Background()
	cursorDir := t.TempDir()
	globalDB := filepath.Join(cursorDir, "User", "globalStorage", "state.vscdb")
	writeGlobalComposerFileHistoryFixtureDB(t, globalDB)

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
	count, err := indexCursorSQLiteFileHistory(ctx, cursorDir, s, tx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected sqlite file-history indexing skipped by size limit, got %d", count)
	}
}
