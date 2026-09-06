package cmd

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/ingest"
)

func TestAutomaticScanCatchesUpAfterUnchangedIndex(t *testing.T) {
	root := t.TempDir()
	command := pinRoots(t, filepath.Join(t.TempDir(), "ccpeek.db"), root)
	writeClaudeSession(t, root, "44444444-4444-4444-4444-444444444444", leakySessionPrompt())
	eng, err := openEngine(context.Background(), command, indexNow(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	// No scan accompanied the first index. An unchanged later pass must still
	// discover the scan backlog, rather than gate work on FilesChanged.
	if err := scanChanged(context.Background(), eng, &ingest.Report{}, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := eng.store.DB().QueryRow(`SELECT COUNT(*) FROM scan_findings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("findings=%d", n)
	}
}

func TestExplicitIndexDoesNotImportDefaultLegacyDatabase(t *testing.T) {
	command := pinRoots(t, filepath.Join(t.TempDir(), "legacy.db"), t.TempDir())
	command.Flags().String("index-file", "", "")
	index := filepath.Join(t.TempDir(), "archive?#.db")
	if err := command.Flags().Set("index-file", index); err != nil {
		t.Fatal(err)
	}
	// pinRoots explicitly sets data-file; clear that marker to model a caller
	// specifying only the new flag, without consulting their default v1 path.
	command.Flags().Lookup("data-file").Changed = false
	legacy, err := resolveDataFile(command)
	if err != nil || legacy != "" {
		t.Fatalf("legacy=%q %v", legacy, err)
	}
	path, err := resolveIndexFile(command, legacy)
	if err != nil || path != index {
		t.Fatalf("index=%q %v", path, err)
	}
}
