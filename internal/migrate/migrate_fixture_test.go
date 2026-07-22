package migrate

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// TestImportV1HistoricalFixtures runs the importer against a real
// database of every schema vintage a v1 install could leave behind
// (testdata/v1, restored from v1's migration-test corpus). No fixture
// source path exists on disk and the v2 store starts empty, so every
// session imports as an orphan; the expectations below are derived
// from each fixture's .sql seed.
func TestImportV1HistoricalFixtures(t *testing.T) {
	ctx := context.Background()

	want := map[string]Report{
		// No source_path column yet; no sidecars.
		"v4-earliest-supported.db": {OrphanSessions: 1, OrphanMessages: 2},
		// First vintage with sessions.source_path and a plans table.
		"v5-source-path-and-plan-data.db": {OrphanSessions: 1, OrphanMessages: 2, OrphanArtifacts: 1},
		// A commands table (no tool_calls yet) and scan_findings without
		// the ignored column: the command imports as a shell tool call,
		// the finding has no ignore decision to carry.
		"v7-legacy-sessions-and-scan-findings.db": {OrphanSessions: 1, OrphanMessages: 2, OrphanToolCalls: 1},
		"v8-session-uniqueness.db":                {OrphanSessions: 1, OrphanMessages: 2},
		"v10-derived-data.db":                     {OrphanSessions: 1, OrphanMessages: 2, OrphanToolCalls: 1},
		// tool_calls and commands both exist; commands must not import
		// twice. The todo list lands as a structured artifact.
		"v13-delete-actions.db": {OrphanSessions: 1, OrphanMessages: 1, OrphanToolCalls: 1, OrphanArtifacts: 1},
		// plans without source_path, and the pre-file_name memories shape
		// (the v14→v15 migration is what added that column).
		"v14-damaged-derived-tables.db": {OrphanSessions: 1, OrphanMessages: 2, OrphanArtifacts: 2},
	}

	fixtures, err := filepath.Glob(filepath.Join("testdata", "v1", "*.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != len(want) {
		t.Fatalf("found %d fixtures, want %d — keep the expectation table in sync", len(fixtures), len(want))
	}

	for _, path := range fixtures {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			expected, ok := want[name]
			if !ok {
				t.Fatalf("no expectation for fixture %s", name)
			}
			store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()

			report, err := ImportV1(ctx, store, path)
			if err != nil {
				t.Fatalf("ImportV1: %v", err)
			}
			if *report != expected {
				t.Fatalf("report = %+v, want %+v", *report, expected)
			}

			count := func(query string) int {
				t.Helper()
				var n int
				if err := store.DB().QueryRowContext(ctx, query).Scan(&n); err != nil {
					t.Fatalf("%s: %v", query, err)
				}
				return n
			}
			before := [4]int{
				count(`SELECT COUNT(*) FROM sessions`),
				count(`SELECT COUNT(*) FROM messages`),
				count(`SELECT COUNT(*) FROM tool_calls`),
				count(`SELECT COUNT(*) FROM artifacts`),
			}
			// Everything the report counted is actually in the store.
			if before[0] != expected.OrphanSessions || before[1] != expected.OrphanMessages ||
				before[2] != expected.OrphanToolCalls || before[3] != expected.OrphanArtifacts {
				t.Fatalf("store rows = %v, want [%d %d %d %d]", before,
					expected.OrphanSessions, expected.OrphanMessages,
					expected.OrphanToolCalls, expected.OrphanArtifacts)
			}

			// Legacy commands import in the shape the commands browser
			// reads.
			if name == "v7-legacy-sessions-and-scan-findings.db" {
				if n := count(`SELECT COUNT(*) FROM tool_calls
					WHERE kind = 'shell'
					  AND json_extract(input_json, '$.command') = 'echo legacy'`); n != 1 {
					t.Errorf("legacy command tool calls = %d, want 1", n)
				}
			}
			// The pre-file_name memory imports under the same MEMORY.md
			// default the v14→v15 migration would have backfilled.
			if name == "v14-damaged-derived-tables.db" {
				if n := count(`SELECT COUNT(*) FROM artifacts
					WHERE kind = 'memory'
					  AND name = '-Users-me-damaged-project/MEMORY.md'`); n != 1 {
					t.Errorf("pre-file_name memory artifacts = %d, want 1", n)
				}
			}

			// Idempotent: a second run must not duplicate anything.
			if _, err := ImportV1(ctx, store, path); err != nil {
				t.Fatalf("re-import: %v", err)
			}
			after := [4]int{
				count(`SELECT COUNT(*) FROM sessions`),
				count(`SELECT COUNT(*) FROM messages`),
				count(`SELECT COUNT(*) FROM tool_calls`),
				count(`SELECT COUNT(*) FROM artifacts`),
			}
			if after != before {
				t.Fatalf("rows after re-import = %v, want %v", after, before)
			}
		})
	}
}
