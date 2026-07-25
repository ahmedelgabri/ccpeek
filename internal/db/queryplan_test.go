package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// planFor returns the joined EXPLAIN QUERY PLAN rows for a query.
func planFor(t *testing.T, store *Store, query string, args ...any) string {
	t.Helper()
	rows, err := store.ReadDB().QueryContext(context.Background(),
		"EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func planStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// The transcript reads message text out of search_docs, because messages
// has no text column. That join MUST be a seek on all three of
// (session_id, doc_type, seq): indexed on session_id alone, SQLite seeks
// the session and then scans every one of its docs once per message in the
// page, which is quadratic in session length — a 6000-message session took
// 2.5s to return a single 1000-message page, versus 6ms with the index.
//
// A plan assertion rather than a timing one: the cost only shows up on
// sessions bigger than a test wants to build, and the failure mode is a
// silently dropped index, which the plan names outright.
func TestTranscriptJoinSeeksItsIndex(t *testing.T) {
	plan := planFor(t, planStore(t), `
		SELECT m.seq, d.text_content
		FROM messages m
		LEFT JOIN search_docs d ON d.session_id = m.session_id
			AND d.doc_type = 'message' AND d.seq = m.seq
		WHERE m.session_id = ? AND m.seq >= ?
		ORDER BY m.seq LIMIT ?`, 1, 0, 1000)

	if !strings.Contains(plan, "idx_search_docs_msg") {
		t.Errorf("transcript join does not use idx_search_docs_msg:\n%s", plan)
	}
	// "SCAN d" (as opposed to "SEARCH d") is the quadratic shape.
	for line := range strings.SplitSeq(plan, "\n") {
		if strings.HasPrefix(line, "SCAN d") {
			t.Errorf("transcript scans search_docs instead of seeking it:\n%s", plan)
		}
	}
}

// Deleting a session's search docs is a session_id-only lookup. The
// composite index leads with session_id, so it serves this too — which is
// why idx_search_docs_session could be dropped rather than kept alongside.
func TestSearchDocDeleteBySessionStaysIndexed(t *testing.T) {
	plan := planFor(t, planStore(t),
		`DELETE FROM search_docs WHERE session_id = ?`, 1)
	if !strings.Contains(plan, "idx_search_docs_msg") {
		t.Errorf("delete-by-session lost its index:\n%s", plan)
	}
}

// The commands browser orders newest-first across the whole corpus and
// pages with LIMIT/OFFSET. Ordering on tc.started_at ALONE lets
// idx_tool_calls_kind (kind, started_at DESC) supply both the filter and
// the order, so the LIMIT short-circuits. The previous
// COALESCE(tc.started_at, se.created_at, ”) was an expression no index
// can serve, which forced a full materialize-and-sort of every shell call
// ever indexed before the LIMIT applied.
func TestCommandsListStopsAtTheLimit(t *testing.T) {
	plan := planFor(t, planStore(t), `
		SELECT tc.command, COALESCE(tc.started_at, ''),
		       a.slug, se.external_id, se.cwd
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE tc.kind = 'shell' AND tc.command <> ''
		ORDER BY tc.started_at DESC, tc.id DESC
		LIMIT ? OFFSET ?`, 100, 0)

	if !strings.Contains(plan, "idx_tool_calls_kind") {
		t.Errorf("commands list does not use idx_tool_calls_kind:\n%s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE FOR ORDER BY") {
		t.Errorf("commands list sorts the whole result set before its LIMIT:\n%s", plan)
	}
}

// The Overview's recent-file-edits feed. Same shape as the commands list:
// the LIMIT must stop the scan, not trim a fully sorted result.
func TestRecentFileEditsStopsAtTheLimit(t *testing.T) {
	plan := planFor(t, planStore(t), `
		SELECT tc.file_path, tc.kind, a.slug, se.external_id,
		       COALESCE(tc.started_at, '')
		FROM tool_calls tc INDEXED BY idx_tool_calls_recent_files
		JOIN sessions se ON se.id = tc.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE tc.kind IN ('file_write', 'file_edit') AND tc.file_path <> ''
		ORDER BY tc.started_at DESC LIMIT 120`)

	if !strings.Contains(plan, "idx_tool_calls_recent_files") {
		t.Errorf("recent-files feed does not use its partial index:\n%s", plan)
	}
	if strings.Contains(plan, "TEMP B-TREE FOR ORDER BY") {
		t.Errorf("recent-files feed sorts everything before its LIMIT:\n%s", plan)
	}
}
