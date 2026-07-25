package secrets

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// BenchmarkScanArtifacts measures the artifact pass over a corpus the size
// of a real first scan — 5000 artifacts of ~2 KiB, every one of them
// changed. Content is secret-free on purpose: the detector's regex cost is
// identical whatever the fetch strategy is, so keeping findings at zero
// leaves the fetch as the signal.
//
// This is what justified paging the content fetch. Measured on this
// machine, over three runs each: ~790ms paged vs ~1073ms when every worker
// issued its own SELECT, and the paged form allocates fewer bytes as well
// — the cursor is back-pressured by the worker limit, so it never holds
// more than one page's worth of rows in flight.
func BenchmarkScanArtifacts(b *testing.B) {
	const (
		artifacts    = 5000
		contentBytes = 2 << 10
	)
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(b.TempDir(), "v2.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	body := strings.Repeat("export PATH=/usr/bin:/bin\n", contentBytes/26+1)
	w, err := store.BeginWrite(ctx)
	if err != nil {
		b.Fatal(err)
	}
	for i := range artifacts {
		if _, err := w.UpsertArtifact(canon.Artifact{
			Agent: "claude-code", Kind: canon.ArtifactShellSnapshot,
			Name:    fmt.Sprintf("snapshot-%d.sh", i),
			Content: fmt.Sprintf("# artifact %d\n%s", i, body),
		}, fmt.Sprintf("h%d", i)); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		b.Fatal(err)
	}

	sc, err := New(store)
	if err != nil {
		b.Fatal(err)
	}

	for b.Loop() {
		b.StopTimer()
		// Every iteration must scan the whole corpus, so forget the state
		// the previous one recorded.
		if _, err := store.DB().ExecContext(ctx, `DELETE FROM scan_state`); err != nil {
			b.Fatal(err)
		}
		state, err := sc.loadState(ctx)
		if err != nil {
			b.Fatal(err)
		}
		var report Report
		b.StartTimer()

		if err := sc.scanArtifacts(ctx, state, &report); err != nil {
			b.Fatal(err)
		}
		if report.ArtifactsScanned != artifacts {
			b.Fatalf("scanned %d artifacts, want %d", report.ArtifactsScanned, artifacts)
		}
	}
}
