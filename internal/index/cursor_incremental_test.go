package index

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func writeCursorJSONLFixture(t *testing.T, cursorDir string) string {
	t.Helper()
	transcriptsDir := filepath.Join(cursorDir, "projects", "cursor-proj", "agent-transcripts")
	if err := os.MkdirAll(transcriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sessionPath := filepath.Join(transcriptsDir, "session-1.jsonl")
	content := `{"role":"user","message":{"role":"user","content":"hello cursor"}}
{"role":"assistant","message":{"role":"assistant","content":"hello"}}`
	if err := os.WriteFile(sessionPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return sessionPath
}

func TestCursorIncrementalAndPruneJSONL(t *testing.T) {
	ctx := context.Background()
	claudeDir := t.TempDir()
	cursorDir := t.TempDir()
	sessionPath := writeCursorJSONLFixture(t, cursorDir)

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(ctx, claudeDir, cursorDir, s, false, io.Discard); err != nil {
		t.Fatal(err)
	}

	changed, err := RunIncrementalWithOptions(ctx, claudeDir, cursorDir, s, DefaultRunOptions)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected unchanged incremental run for stable cursor fixture")
	}

	if err := os.WriteFile(sessionPath, []byte(`{"role":"user","message":{"role":"user","content":"changed"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = RunIncrementalWithOptions(ctx, claudeDir, cursorDir, s, DefaultRunOptions)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed incremental run after transcript modification")
	}

	if err := os.Remove(sessionPath); err != nil {
		t.Fatal(err)
	}
	if err := Prune(ctx, claudeDir, cursorDir, s, io.Discard); err != nil {
		t.Fatal(err)
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if p.DirName == "cursor-proj" {
			t.Fatalf("expected cursor project to be pruned after transcript deletion, got %+v", p)
		}
	}
}

func TestCursorSnapshotDirectoryFingerprintStable(t *testing.T) {
	ctx := context.Background()
	claudeDir := t.TempDir()
	cursorDir := t.TempDir()

	snapDir := filepath.Join(cursorDir, "snapshots", "not-a-git-snapshot")
	if err := os.MkdirAll(snapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "state.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(ctx, claudeDir, cursorDir, s, false, io.Discard); err != nil {
		t.Fatal(err)
	}

	changed, err := RunIncrementalWithOptions(ctx, claudeDir, cursorDir, s, DefaultRunOptions)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected unchanged incremental run for unchanged snapshot directory")
	}

	if err := os.WriteFile(filepath.Join(snapDir, "state.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = RunIncrementalWithOptions(ctx, claudeDir, cursorDir, s, DefaultRunOptions)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed incremental run after snapshot directory content changed")
	}
}

func TestCursorSQLiteModeToggleTriggersCleanup(t *testing.T) {
	ctx := context.Background()
	claudeDir := t.TempDir()
	cursorDir := t.TempDir()
	globalDB := filepath.Join(cursorDir, "User", "globalStorage", "state.vscdb")
	writeGlobalComposerFixtureDB(t, globalDB)

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	enabled := DefaultRunOptions
	if err := RunWithOptions(ctx, claudeDir, cursorDir, s, true, io.Discard, enabled); err != nil {
		t.Fatal(err)
	}

	projects, err := s.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundSQLiteProject := false
	for _, p := range projects {
		if strings.HasPrefix(p.DirName, "cursor-sqlite-") {
			foundSQLiteProject = true
		}
	}
	if !foundSQLiteProject {
		t.Fatal("expected at least one cursor sqlite project after enabled indexing")
	}

	disabled := DefaultRunOptions
	disabled.IncludeCursorSQLite = false
	changed, err := RunIncrementalWithOptions(ctx, claudeDir, cursorDir, s, disabled)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed incremental run when sqlite mode toggles from enabled to disabled")
	}

	projects, err = s.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range projects {
		if strings.HasPrefix(p.DirName, "cursor-sqlite-") {
			t.Fatalf("expected sqlite projects removed after disabling sqlite indexing, found %q", p.DirName)
		}
	}
}
