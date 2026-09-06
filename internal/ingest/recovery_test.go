package ingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func isolatedOptions(root string) Options {
	return Options{ConfigRoots: map[canon.AgentSlug][]string{claude.Slug: {root}}, Home: "/nonexistent-home", Getenv: func(string) string { return "" }}
}

func writeSource(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func usageLine(tokens int) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":"m1","timestamp":"2026-07-01T00:00:00Z","message":{"id":"content1","role":"assistant","model":"claude-sonnet-4-20250514","content":[{"type":"text","text":"answer"}],"usage":{"input_tokens":%d,"output_tokens":10}}}`+"\n", tokens)
}

func TestInterruptedPassRepairsPopulatedRollups(t *testing.T) {
	t.Run("index", func(t *testing.T) { testInterruptedPassRepair(t, false) })
	t.Run("repair without sources", func(t *testing.T) { testInterruptedPassRepair(t, true) })
}

func testInterruptedPassRepair(t *testing.T, repairOnly bool) {
	t.Helper()
	runner, store := newRunner(t)
	root := t.TempDir()
	path := filepath.Join(root, "projects", "project", "review.jsonl")
	opts := isolatedOptions(root)
	writeSource(t, path, usageLine(100))
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	writeSource(t, path, usageLine(200))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	opts.Progress = func(p Progress) {
		if !p.Root && p.Changed > 0 {
			cancel()
		}
	}
	if _, err := runner.Run(ctx, opts); err == nil {
		t.Fatal("expected interruption")
	}
	if repairOnly {
		// Recovery must use committed records, not re-open their sources.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := runner.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
	} else {
		opts.Progress = nil
		report, err := runner.Run(context.Background(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if report.FilesChanged != 0 {
			t.Fatalf("unexpected reparse: %+v", report)
		}
	}
	if dirty, err := store.DerivedDirty(context.Background()); err != nil || dirty {
		t.Fatalf("derived state not repaired: dirty=%v err=%v", dirty, err)
	}
	raw := queryInt(t, store, `SELECT SUM(input_tokens) FROM message_usage`)
	rolled := queryInt(t, store, `SELECT SUM(input_tokens) FROM rollup_usage_daily`)
	if raw != 200 || rolled != raw {
		t.Fatalf("raw=%d rolled=%d", raw, rolled)
	}
}

func TestWatchPassRunsWithoutChangedSources(t *testing.T) {
	runner, _ := newRunner(t)
	opts := isolatedOptions(t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	passes, changes := 0, 0
	opts.WatchPass = func() {
		passes++
		if passes == 2 {
			cancel()
		}
	}
	err := runner.poll(ctx, opts, pollTimings{
		idle: time.Millisecond, fast: time.Millisecond,
		hotFor: time.Second, debounce: time.Millisecond,
	}, func(*Report) { changes++ })
	if !errors.Is(err, context.Canceled) || passes != 2 || changes != 0 {
		t.Fatalf("watch passes=%d changes=%d err=%v", passes, changes, err)
	}
}

func TestPruneRetainsInaccessibleSourcesAndMissingRoots(t *testing.T) {
	for _, missingRoot := range []bool{false, true} {
		t.Run(fmt.Sprint(missingRoot), func(t *testing.T) {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("requires Unix permissions")
			}
			runner, store := newRunner(t)
			root := filepath.Join(t.TempDir(), "root")
			path := filepath.Join(root, "projects", "project", "review.jsonl")
			opts := isolatedOptions(root)
			writeSource(t, path, usageLine(100))
			if _, err := runner.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			if missingRoot {
				if err := os.Rename(root, root+"-offline"); err != nil {
					t.Fatal(err)
				}
			} else {
				parent := filepath.Dir(path)
				if err := os.Chmod(parent, 0); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(parent, 0o700)
				if _, err := os.Stat(path); !os.IsPermission(err) {
					t.Fatalf("expected permission denial: %v", err)
				}
			}
			opts.Prune = true
			if _, err := runner.Run(context.Background(), opts); err != nil {
				t.Fatal(err)
			}
			if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 1 {
				t.Fatalf("retained sessions=%d", n)
			}
		})
	}
}

func TestRebuildRetainsArchivedSourcesAndCreatesBackup(t *testing.T) {
	runner, store := newRunner(t)
	root := t.TempDir()
	path := filepath.Join(root, "projects", "project", "review.jsonl")
	opts := isolatedOptions(root)
	writeSource(t, path, usageLine(100))
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w, err := store.BeginWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpsertSession(canon.Session{Agent: claude.Slug, ExternalID: "imported", Origin: canon.OriginImportedV1}, "imported-v1"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	opts.Rebuild = true
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	var archivePath string
	if err := store.DB().QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&archivePath); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(archivePath + ".before-rebuild-*.db")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups=%v err=%v", backups, err)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 2 {
		t.Fatalf("sessions=%d", n)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM messages`); n != 1 {
		t.Fatalf("messages=%d", n)
	}
}

func TestEmptyHistoryAndTaskDirectoryReplacePriorContent(t *testing.T) {
	runner, store := newRunner(t)
	root := t.TempDir()
	opts := isolatedOptions(root)
	history := filepath.Join(root, "history.jsonl")
	task := filepath.Join(root, "tasks", "group", "1.json")
	writeSource(t, history, "{\"display\":\"old prompt\",\"timestamp\":1751443200000}\n")
	writeSource(t, task, `{"subject":"old task"}`)
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	writeSource(t, history, "")
	if err := os.Remove(task); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM history`); n != 0 {
		t.Fatalf("stale history=%d", n)
	}
	if content := queryString(t, store, `SELECT content FROM artifacts WHERE kind='task_group'`); content != "" {
		t.Fatalf("stale task=%q", content)
	}
}
