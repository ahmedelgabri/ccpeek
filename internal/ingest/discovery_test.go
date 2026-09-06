package ingest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/opencode"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func TestOpenCodeIncompleteDiscoveryRetainsPruneCandidates(t *testing.T) {
	for _, failure := range []string{"bad-database", "missing-table", "unreadable-project"} {
		t.Run(failure, func(t *testing.T) {
			runner, store := newRunner(t)
			runner.adapters = append(runner.adapters, opencode.New())
			root := t.TempDir()
			opts := Options{Prune: true, ConfigRoots: map[canon.AgentSlug][]string{opencode.Slug: {root}}, Home: "/nonexistent-home", Getenv: func(string) string { return "" }}
			old := filepath.Join(root, "storage", "session", "good", "old.json")
			writeSource(t, old, `{"id":"old","title":"retained"}`)
			if _, err := runner.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(old); err != nil {
				t.Fatal(err)
			}
			writeSource(t, filepath.Join(root, "storage", "session", "good", "new.json"), `{"id":"new","title":"healthy source"}`)
			bad := filepath.Join(root, "opencode-bad.db")
			switch failure {
			case "bad-database":
				writeSource(t, bad, "not sqlite")
			case "missing-table":
				database, err := sql.Open("sqlite", bad)
				if err != nil {
					t.Fatal(err)
				}
				_, err = database.Exec(`CREATE TABLE unrelated(id INTEGER)`)
				database.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "unreadable-project":
				bad = filepath.Join(root, "storage", "session", "bad")
				if err := os.MkdirAll(bad, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(bad, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(bad, 0o700) })
				if _, err := os.ReadDir(bad); err == nil {
					t.Skip("permission bits are not enforced")
				}
			}
			report, err := runner.Run(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "partial" {
				t.Fatalf("report=%+v", report)
			}
			var warned bool
			for _, issue := range report.Issues {
				if issue.SourcePath == bad && issue.Severity == canon.SeverityWarn {
					warned = true
				}
			}
			if !warned {
				t.Fatalf("missing per-path warning: %+v", report.Issues)
			}
			if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 2 {
				t.Fatalf("healthy or retained source lost: %d sessions", n)
			}
			if failure == "unreadable-project" {
				if err := os.Chmod(bad, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Remove(bad); err != nil {
				t.Fatal(err)
			}
			report, err = runner.Run(context.Background(), opts)
			if err != nil || report.Status != "ok" {
				t.Fatalf("recovered: %+v %v", report, err)
			}
			if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 1 {
				t.Fatalf("complete discovery did not prune: %d", n)
			}
		})
	}
}
