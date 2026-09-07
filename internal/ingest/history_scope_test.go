package ingest

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

type failingHistoryAdapter struct{ agent.Adapter }

func (a failingHistoryAdapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	if err := a.Adapter.Parse(ctx, src, sink); err != nil {
		return err
	}
	if src.HistorySnapshot {
		return fmt.Errorf("synthetic failure after history emission")
	}
	return nil
}

func TestOnlyHistorySnapshotsReplaceLegacyHistory(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		history, fail    bool
	}{
		{name: "session", path: "projects/p/session.jsonl", body: usageLine(100)},
		{name: "task", path: "tasks/group/1.json", body: `{"subject":"a task"}`},
		{name: "artifact", path: "plans/plan.md", body: "a plan"},
		{name: "history", path: "history.jsonl", body: "{\"display\":\"replacement prompt\",\"timestamp\":1751443200000}\n", history: true},
		{name: "empty history", path: "history.jsonl", history: true},
		{name: "failed history", path: "history.jsonl", body: "{\"display\":\"replacement prompt\",\"timestamp\":1751443200000}\n", history: true, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, store := newRunner(t)
			ctx := context.Background()
			root := t.TempDir()
			opts := isolatedOptions(root)
			historyPath := filepath.Join(root, "history.jsonl")
			w, err := store.BeginWrite(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer w.Rollback()
			for _, entry := range []struct {
				agent         canon.AgentSlug
				path, display string
			}{
				{claude.Slug, "", "legacy prompt"},
				{claude.Slug, historyPath, "prior snapshot"},
				{claude.Slug, filepath.Join(root, "another-history.jsonl"), "separate snapshot"},
				{"pi", "", "other agent legacy"},
				{"pi", historyPath, "other agent snapshot"},
			} {
				if err := w.InsertHistory(canon.HistoryEntry{Agent: entry.agent, Display: entry.display, Timestamp: time.Unix(1751443200, 0)}, entry.path); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Commit(); err != nil {
				t.Fatal(err)
			}
			writeSource(t, filepath.Join(root, tc.path), tc.body)
			if tc.fail {
				runner.adapters = []agent.Adapter{failingHistoryAdapter{claude.New()}}
			}
			for pass := 0; pass < 2; pass++ {
				opts.Rebuild = pass == 1 // Rebuild must not turn an unrelated source into history replacement.
				report, err := runner.Run(ctx, opts)
				if err != nil {
					t.Fatal(err)
				}
				if tc.fail && report.Status != "partial" {
					t.Fatalf("failed parse: %+v", report)
				}
				wantOld := 1
				if tc.history && !tc.fail {
					wantOld = 0
				}
				for _, display := range []string{"legacy prompt", "prior snapshot"} {
					var n int
					if err := store.ReadDB().QueryRow(`SELECT COUNT(*) FROM history WHERE display=?`, display).Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n != wantOld {
						t.Errorf("pass %d: %q count=%d want %d", pass, display, n, wantOld)
					}
				}
				for _, display := range []string{"other agent legacy", "other agent snapshot", "separate snapshot"} {
					var n int
					if err := store.ReadDB().QueryRow(`SELECT COUNT(*) FROM history WHERE display=?`, display).Scan(&n); err != nil {
						t.Fatal(err)
					}
					if n != 1 {
						t.Errorf("pass %d: unrelated history %q count=%d", pass, display, n)
					}
				}
				wantNew := 0
				if tc.history && tc.body != "" && !tc.fail {
					wantNew = 1
				}
				if n := queryInt(t, store, `SELECT COUNT(*) FROM history WHERE display='replacement prompt'`); n != wantNew {
					t.Errorf("replacement count=%d want %d", n, wantNew)
				}
			}
		})
	}
}
