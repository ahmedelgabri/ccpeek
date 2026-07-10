package db

import (
	"context"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func beginWrite(t *testing.T, s *Store) *Writer {
	t.Helper()
	w, err := s.BeginWrite(context.Background())
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	return w
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func testSession(id string) canon.Session {
	return canon.Session{
		Agent:      "claude-code",
		ExternalID: id,
		Title:      "hello",
		CreatedAt:  time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		ModifiedAt: time.Date(2026, 7, 1, 11, 0, 0, 0, time.UTC),
		CWD:        "/home/u/proj",
		GitBranch:  "main",
		SourcePath: "/roots/claude/projects/x/" + id + ".jsonl",
	}
}

func TestUpsertSessionIdempotent(t *testing.T) {
	s, _ := openTemp(t)
	w := beginWrite(t, s)

	id1, err := w.UpsertSession(testSession("sess-a"), "hash1")
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	sess := testSession("sess-a")
	sess.Title = "updated title"
	id2, err := w.UpsertSession(sess, "hash2")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("row id changed on upsert: %d → %d", id1, id2)
	}
	if err := w.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if n := count(t, s, `SELECT COUNT(*) FROM sessions`); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
	var title, hash string
	if err := s.db.QueryRow(`SELECT title, content_hash FROM sessions`).Scan(&title, &hash); err != nil {
		t.Fatal(err)
	}
	if title != "updated title" || hash != "hash2" {
		t.Fatalf("title=%q hash=%q after upsert", title, hash)
	}
}

func TestUsageDedupeAcrossSessions(t *testing.T) {
	s, _ := openTemp(t)
	w := beginWrite(t, s)

	cost := 0.42
	usage := &canon.Usage{
		InputTokens: 1000, OutputTokens: 50,
		CacheReadTokens: 9000, CacheWriteTokens: 200,
		ReportedCostUSD: &cost, RequestID: "req_1",
	}
	msg := canon.Message{
		Seq: 0, ExternalID: "msg_abc", Role: canon.RoleAssistant,
		CreatedAt: time.Now(), Model: "claude-sonnet-5",
		Content: []byte(`{"role":"assistant"}`), Usage: usage,
	}

	// Original session records the usage.
	origID, err := w.UpsertSession(testSession("sess-a"), "h")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertMessage(origID, "claude-code", msg); err != nil {
		t.Fatal(err)
	}

	// A resumed session repeats the same assistant entry (same external
	// message id + request id): transcript row lands, usage must not.
	resumedID, err := w.UpsertSession(testSession("sess-b"), "h")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertMessage(resumedID, "claude-code", msg); err != nil {
		t.Fatal(err)
	}

	// Different request id → separate usage.
	fresh := msg
	fresh.Seq = 1
	fresh.ExternalID = "msg_def"
	freshUsage := *usage
	freshUsage.RequestID = "req_2"
	fresh.Usage = &freshUsage
	if err := w.InsertMessage(resumedID, "claude-code", fresh); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	if n := count(t, s, `SELECT COUNT(*) FROM messages`); n != 3 {
		t.Errorf("messages = %d, want 3 (transcripts stay complete)", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM message_usage`); n != 2 {
		t.Errorf("message_usage = %d, want 2 (duplicate not double-counted)", n)
	}
	var total int64
	if err := s.db.QueryRow(`SELECT SUM(input_tokens) FROM message_usage`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 2000 {
		t.Errorf("summed input tokens = %d, want 2000", total)
	}
}

func TestLinkArtifactResolvedAndPending(t *testing.T) {
	s, _ := openTemp(t)
	w := beginWrite(t, s)

	if _, err := w.UpsertSession(testSession("sess-a"), "h"); err != nil {
		t.Fatal(err)
	}
	artID, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactTodoList,
		Name: "sess-a-agent-xyz.json", Content: "[]",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := w.LinkArtifact(artID, canon.ArtifactLink{
		Agent: "claude-code", ArtifactKind: canon.ArtifactTodoList,
		ArtifactName: "sess-a-agent-xyz.json", SessionExternalID: "sess-a",
		Relation: canon.LinkProducedBy, Evidence: canon.EvidenceFilenameUUID,
	})
	if err != nil || !resolved {
		t.Fatalf("link to existing session: resolved=%v err=%v", resolved, err)
	}

	resolved, err = w.LinkArtifact(artID, canon.ArtifactLink{
		Agent: "claude-code", ArtifactKind: canon.ArtifactTodoList,
		ArtifactName: "sess-a-agent-xyz.json", SessionExternalID: "sess-future",
		Relation: canon.LinkProducedBy, Evidence: canon.EvidenceFilenameUUID,
	})
	if err != nil || resolved {
		t.Fatalf("link to missing session: resolved=%v err=%v, want parked", resolved, err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	if n := count(t, s, `SELECT COUNT(*) FROM artifact_sessions`); n != 1 {
		t.Fatalf("artifact_sessions = %d, want 1", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pending_artifact_links`); n != 1 {
		t.Fatalf("pending_artifact_links = %d, want 1", n)
	}

	// The missing session arrives in a later run; pending link resolves.
	w2 := beginWrite(t, s)
	if _, err := w2.UpsertSession(testSession("sess-future"), "h"); err != nil {
		t.Fatal(err)
	}
	if err := w2.Commit(); err != nil {
		t.Fatal(err)
	}
	resolvedN, remaining, err := s.ResolvePending(context.Background())
	if err != nil {
		t.Fatalf("ResolvePending: %v", err)
	}
	if resolvedN != 1 || remaining != 0 {
		t.Fatalf("ResolvePending = (%d resolved, %d remaining), want (1, 0)", resolvedN, remaining)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM artifact_sessions`); n != 2 {
		t.Fatalf("artifact_sessions after resolve = %d, want 2", n)
	}
}

func TestSessionRelationPendingLifecycle(t *testing.T) {
	s, _ := openTemp(t)
	w := beginWrite(t, s)

	if _, err := w.UpsertSession(testSession("child"), "h"); err != nil {
		t.Fatal(err)
	}
	rel := canon.SessionRelation{
		Agent: "claude-code", FromExternalID: "child",
		ToExternalID: "parent", Kind: canon.RelResumedFrom,
	}
	resolved, err := w.AddSessionRelation(rel)
	if err != nil || resolved {
		t.Fatalf("relation with missing parent: resolved=%v err=%v, want parked", resolved, err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	w2 := beginWrite(t, s)
	if _, err := w2.UpsertSession(testSession("parent"), "h"); err != nil {
		t.Fatal(err)
	}
	if err := w2.Commit(); err != nil {
		t.Fatal(err)
	}

	resolvedN, remaining, err := s.ResolvePending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resolvedN != 1 || remaining != 0 {
		t.Fatalf("ResolvePending = (%d, %d), want (1, 0)", resolvedN, remaining)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM session_relations WHERE kind = 'resumed_from'`); n != 1 {
		t.Fatalf("session_relations = %d, want 1", n)
	}
}

func TestClearSessionChildren(t *testing.T) {
	s, _ := openTemp(t)
	w := beginWrite(t, s)

	id, err := w.UpsertSession(testSession("sess-a"), "h")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertMessage(id, "claude-code", canon.Message{
		Seq: 0, Role: canon.RoleUser, Content: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertToolCall(id, canon.ToolCall{Seq: 0, Name: "Bash", Kind: canon.ToolShell}); err != nil {
		t.Fatal(err)
	}
	if err := w.ClearSessionChildren(id); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertMessage(id, "claude-code", canon.Message{
		Seq: 0, Role: canon.RoleUser, Content: []byte(`{"v":2}`),
	}); err != nil {
		t.Fatalf("reinsert after clear: %v", err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	if n := count(t, s, `SELECT COUNT(*) FROM messages`); n != 1 {
		t.Errorf("messages = %d, want 1", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM tool_calls`); n != 0 {
		t.Errorf("tool_calls = %d, want 0", n)
	}
}

func TestInsertHistoryResolvesSession(t *testing.T) {
	s, _ := openTemp(t)
	w := beginWrite(t, s)
	if _, err := w.UpsertSession(testSession("sess-a"), "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertHistory(canon.HistoryEntry{
		Agent: "claude-code", Display: "fix the tests",
		Timestamp: time.Now(), SessionExternalID: "sess-a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertHistory(canon.HistoryEntry{
		Agent: "claude-code", Display: "orphan prompt", Timestamp: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM history WHERE session_id IS NOT NULL`); n != 1 {
		t.Errorf("linked history = %d, want 1", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM history`); n != 2 {
		t.Errorf("history = %d, want 2", n)
	}
}
