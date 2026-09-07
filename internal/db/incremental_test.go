package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

type countingPricer struct{ calls int }

func (p *countingPricer) Lookup(string) (pricing.Rate, bool) {
	p.calls++
	return pricing.Rate{Input: 0.000001, Output: 0.00001}, true
}

func rollupSnapshot(t *testing.T, store *Store) string {
	t.Helper()
	var out []string
	for _, table := range []string{"rollup_usage_daily", "rollup_session_days"} {
		rows, err := store.DB().Query(`SELECT * FROM ` + table + ` ORDER BY 1,2,3,4`)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			dest := make([]any, len(cols))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(values))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	return strings.Join(out, "\n")
}

func TestIncrementalRollupsMatchFullRebuild(t *testing.T) {
	ctx := context.Background()
	store, _ := openTemp(t)
	pricer := &countingPricer{}
	put := func(name, cwd string, day int, tokens int64) {
		t.Helper()
		w, err := store.BeginWrite(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Rollback()
		at := time.Date(2026, 7, day, 0, 0, 0, 0, time.UTC)
		id, err := w.UpsertSession(canon.Session{Agent: "claude-code", ExternalID: name, SourcePath: "/" + name, CWD: cwd, CreatedAt: at}, "hash")
		if err != nil {
			t.Fatal(err)
		}
		if err := w.ClearSessionChildren(id); err != nil {
			t.Fatal(err)
		}
		if err := w.InsertMessage(id, "claude-code", canon.Message{Seq: 0, Role: canon.RoleAssistant, Model: "model", CreatedAt: at, Usage: &canon.Usage{InputTokens: tokens, OutputTokens: 20}}); err != nil {
			t.Fatal(err)
		}
		if err := w.RecordSourceFile("/"+name, "claude-code", "hash", "", "", 1); err != nil {
			t.Fatal(err)
		}
		if err := w.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func() {
		t.Helper()
		if err := store.RefreshWorkspaces(ctx); err != nil {
			t.Fatal(err)
		}
		if err := store.RefreshRollups(ctx, pricer); err != nil {
			t.Fatal(err)
		}
	}
	equivalent := func() {
		t.Helper()
		incremental := rollupSnapshot(t, store)
		if err := store.RegenerateRollups(ctx, pricer); err != nil {
			t.Fatal(err)
		}
		full := rollupSnapshot(t, store)
		if full != incremental {
			t.Fatalf("incremental:\n%s\nfull:\n%s", incremental, full)
		}
	}
	put("a", "/first", 1, 100)
	put("b", "/second", 2, 200)
	put("c", "/third", 3, 300)
	refresh()
	equivalent()
	stableID := count(t, store, `SELECT id FROM workspaces WHERE canonical_path='/third'`)
	pricer.calls = 0
	put("a", "/renamed", 4, 400)
	refresh()
	if pricer.calls != 1 {
		t.Fatalf("repriced %d rows, want only the changed day", pricer.calls)
	}
	if id := count(t, store, `SELECT id FROM workspaces WHERE canonical_path='/third'`); id != stableID {
		t.Fatalf("workspace changed from %d to %d", stableID, id)
	}
	equivalent()
	// Keep another session on the old day to exercise deletion plus regrouping.
	put("d", "/second", 2, 500)
	refresh()
	equivalent()
	if _, err := store.PruneMissingSources(ctx, func(path string) bool { return path != "/b" }); err != nil {
		t.Fatal(err)
	}
	refresh()
	equivalent()
	if _, err := store.PruneMissingSources(ctx, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	refresh()
	equivalent()
	if n := count(t, store, `SELECT COUNT(*) FROM rollup_usage_daily`); n != 0 {
		t.Fatalf("stale final rollups=%d", n)
	}
}
