package server

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestReindexAndMaybeScanRunsScanWhenRequested(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	plansDir := filepath.Join(root, "plans")
	if err := os.MkdirAll(plansDir, 0o755); err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(plansDir, "secret.md")
	if err := os.WriteFile(planPath, []byte("no secrets yet"), 0o644); err != nil {
		t.Fatal(err)
	}

	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := index.Run(ctx, root, db, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearScanFindings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("AKIA2OGYBAH6QLHAMZXB"), 0o644); err != nil {
		t.Fatal(err)
	}

	changed, err := reindexAndMaybeScan(ctx, root, db, true)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changes on first incremental reindex")
	}
	count, err := db.ScanFindingCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("expected secret scan findings after watch-mode reindex")
	}
}
