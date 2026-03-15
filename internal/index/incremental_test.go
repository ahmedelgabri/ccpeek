package index

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

// setupTestDir creates a minimal claude-dir with a plan file for testing.
func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	plansDir := filepath.Join(dir, "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(plansDir, "alpha.md"),
		[]byte("# Alpha Plan\nThis is the alpha plan."),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(plansDir, "beta.md"),
		[]byte("# Beta Plan\nThis is the beta plan."),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestIncrementalSkipsUnchangedFiles(t *testing.T) {
	dir := setupTestDir(t)
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// First full index
	if err := Run(context.Background(), dir, s, false, io.Discard); err != nil {
		t.Fatal("initial Run:", err)
	}

	plans, err := s.ListPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans after initial index, got %d", len(plans))
	}

	// Incremental with no changes
	changed, err := RunIncremental(context.Background(), dir, s)
	if err != nil {
		t.Fatal("RunIncremental:", err)
	}
	if changed {
		t.Error("expected no changes on second run, but got changed=true")
	}

	// Plans should still be there
	plans, err = s.ListPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Errorf("expected 2 plans after incremental (no changes), got %d", len(plans))
	}
}

func TestIncrementalReindexesChangedFiles(t *testing.T) {
	dir := setupTestDir(t)
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(context.Background(), dir, s, false, io.Discard); err != nil {
		t.Fatal("initial Run:", err)
	}

	// Verify initial state
	_, content, err := s.GetPlan(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if content != "# Alpha Plan\nThis is the alpha plan." {
		t.Fatalf("unexpected initial content: %q", content)
	}

	// Modify alpha.md
	if err := os.WriteFile(
		filepath.Join(dir, "plans", "alpha.md"),
		[]byte("# Alpha Plan v2\nUpdated content."),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	// Incremental should detect the change
	changed, err := RunIncremental(context.Background(), dir, s)
	if err != nil {
		t.Fatal("RunIncremental:", err)
	}
	if !changed {
		t.Error("expected changes after modifying alpha.md")
	}

	// Verify updated content
	plan, content, err := s.GetPlan(context.Background(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Title != "Alpha Plan v2" {
		t.Errorf("expected updated title 'Alpha Plan v2', got %q", plan.Title)
	}
	if content != "# Alpha Plan v2\nUpdated content." {
		t.Errorf("expected updated content, got %q", content)
	}

	// Beta should be untouched
	plans, err := s.ListPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 2 {
		t.Errorf("expected 2 plans, got %d", len(plans))
	}
}

func TestIncrementalRetainsDeletedSourceData(t *testing.T) {
	dir := setupTestDir(t)
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(context.Background(), dir, s, false, io.Discard); err != nil {
		t.Fatal("initial Run:", err)
	}

	plans, _ := s.ListPlans(context.Background())
	if len(plans) != 2 {
		t.Fatalf("expected 2 plans, got %d", len(plans))
	}

	// Delete alpha.md from disk
	if err := os.Remove(filepath.Join(dir, "plans", "alpha.md")); err != nil {
		t.Fatal(err)
	}

	// Incremental should not remove the alpha plan from DB
	_, err = RunIncremental(context.Background(), dir, s)
	if err != nil {
		t.Fatal("RunIncremental:", err)
	}

	plans, _ = s.ListPlans(context.Background())
	if len(plans) != 2 {
		t.Errorf("expected 2 plans (deleted source retained), got %d", len(plans))
	}
}

func TestPruneRemovesDeletedSourceData(t *testing.T) {
	dir := setupTestDir(t)
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(context.Background(), dir, s, false, io.Discard); err != nil {
		t.Fatal("initial Run:", err)
	}

	// Delete alpha.md from disk
	if err := os.Remove(filepath.Join(dir, "plans", "alpha.md")); err != nil {
		t.Fatal(err)
	}

	// Prune should remove the alpha plan from DB
	if err := Prune(context.Background(), dir, s, io.Discard); err != nil {
		t.Fatal("Prune:", err)
	}

	plans, _ := s.ListPlans(context.Background())
	if len(plans) != 1 {
		t.Errorf("expected 1 plan after prune, got %d", len(plans))
	}
	if len(plans) > 0 && plans[0].FileName != "beta.md" {
		t.Errorf("expected beta.md to remain, got %q", plans[0].FileName)
	}
}

func TestRebuildClearsEverything(t *testing.T) {
	dir := setupTestDir(t)
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(context.Background(), dir, s, false, io.Discard); err != nil {
		t.Fatal("initial Run:", err)
	}

	// Delete alpha.md
	if err := os.Remove(filepath.Join(dir, "plans", "alpha.md")); err != nil {
		t.Fatal(err)
	}

	// Rebuild should only have beta (alpha is gone from disk)
	if err := Run(context.Background(), dir, s, true, io.Discard); err != nil {
		t.Fatal("rebuild Run:", err)
	}

	plans, _ := s.ListPlans(context.Background())
	if len(plans) != 1 {
		t.Errorf("expected 1 plan after rebuild, got %d", len(plans))
	}
}

func TestIncrementalNewFileAdded(t *testing.T) {
	dir := setupTestDir(t)
	s, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Run(context.Background(), dir, s, false, io.Discard); err != nil {
		t.Fatal("initial Run:", err)
	}

	// Add a new plan file
	if err := os.WriteFile(
		filepath.Join(dir, "plans", "gamma.md"),
		[]byte("# Gamma Plan\nA new plan."),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	changed, err := RunIncremental(context.Background(), dir, s)
	if err != nil {
		t.Fatal("RunIncremental:", err)
	}
	if !changed {
		t.Error("expected changes after adding gamma.md")
	}

	plans, _ := s.ListPlans(context.Background())
	if len(plans) != 3 {
		t.Errorf("expected 3 plans after adding gamma.md, got %d", len(plans))
	}
}
