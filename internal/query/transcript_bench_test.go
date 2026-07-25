package query

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// BenchmarkTranscriptPage measures one page of a long session's transcript
// — what the SPA fetches on every transcript mount and again for each page
// it scrolls into, and what `ccpeek query transcript` and the MCP
// transcript tool return.
//
// Message text lives in search_docs (messages has no text column), so the
// query LEFT JOINs it on (session_id, doc_type, seq). Without an index
// covering all three, SQLite seeks by session_id alone and then scans
// every one of the session's docs for EVERY message in the page — cost
// quadratic in session length. idx_search_docs_msg is what makes the join
// a seek.
func BenchmarkTranscriptPage(b *testing.B) {
	const messages = 6000
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(b.TempDir(), "v2.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	w, err := store.BeginWrite(ctx)
	if err != nil {
		b.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "bench-session",
	}, "h")
	if err != nil {
		b.Fatal(err)
	}
	for i := range messages {
		text := fmt.Sprintf("message %d: refactor the handler and explain the tradeoffs", i)
		if err := w.InsertMessage(sessID, "claude-code", canon.Message{
			Seq: i, Role: canon.RoleUser, Text: text,
			Content: []byte(`{"role":"user"}`),
		}); err != nil {
			b.Fatal(err)
		}
		if err := w.InsertSearchDoc(sessID, 0, "message", i, "", text); err != nil {
			b.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		b.Fatal(err)
	}

	svc := New(store, nil)
	b.ResetTimer()
	for b.Loop() {
		page, err := svc.Transcript(ctx, "claude-code", "bench-session",
			TranscriptOptions{Limit: 1000})
		if err != nil {
			b.Fatal(err)
		}
		if len(page) != 1000 {
			b.Fatalf("page = %d messages, want 1000", len(page))
		}
		if page[0].Text == "" {
			b.Fatal("transcript text did not come back")
		}
	}
}
