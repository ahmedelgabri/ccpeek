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
