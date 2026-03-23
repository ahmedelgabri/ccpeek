package index

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func createGitRepoWithCommit(t *testing.T, repoDir string) {
	t.Helper()
	runGit(t, repoDir, "init")
	runGit(t, repoDir, "config", "user.email", "ccpeek-tests@example.com")
	runGit(t, repoDir, "config", "user.name", "CCPeek Tests")
	if err := os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoDir, "add", "main.go")
	runGit(t, repoDir, "commit", "-m", "initial commit")
}

func TestReadCursorSnapshotDetail(t *testing.T) {
	repoDir := t.TempDir()
	createGitRepoWithCommit(t, repoDir)

	detail, err := readCursorSnapshotDetail("snapshot-1", filepath.Join(repoDir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if detail.SnapshotID != "snapshot-1" {
		t.Fatalf("expected snapshot id snapshot-1, got %q", detail.SnapshotID)
	}
	if detail.CommitHash == "" {
		t.Fatal("expected commit hash in snapshot detail")
	}
	if detail.CommitTimestampMs == 0 {
		t.Fatal("expected commit timestamp in snapshot detail")
	}
	if len(detail.Files) == 0 {
		t.Fatal("expected changed files in snapshot detail")
	}
}

func TestIndexCursorSnapshotsInsertsCursorEntry(t *testing.T) {
	ctx := context.Background()
	repoDir := t.TempDir()
	createGitRepoWithCommit(t, repoDir)

	cursorDir := t.TempDir()
	snapshotDir := filepath.Join(cursorDir, "snapshots", "snapshot-1")
	if err := os.MkdirAll(filepath.Dir(snapshotDir), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "clone", "--bare", repoDir, snapshotDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare failed: %v\n%s", err, string(out))
	}

	s, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count, err := indexCursorSnapshots(ctx, cursorDir, s, tx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one indexed cursor snapshot, got %d", count)
	}

	snapshots, err := s.ListShellSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected one snapshot in store, got %d", len(snapshots))
	}
	if snapshots[0].Kind != cursorSnapshotKindWorkspaceGit {
		t.Fatalf("expected kind %q, got %q", cursorSnapshotKindWorkspaceGit, snapshots[0].Kind)
	}
	if snapshots[0].Source != model.SourceCursor {
		t.Fatalf("expected cursor source snapshot, got %q", snapshots[0].Source)
	}
}
