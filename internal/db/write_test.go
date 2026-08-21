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

// parkArtifactLink writes one task-group artifact and parks a link from
// it to each of the given (not yet indexed) sessions, committing the
// write. It is the preamble of every pending-link lifecycle test, which
// are about what happens AFTER a link is parked.
func parkArtifactLink(t *testing.T, s *Store, sessionIDs ...string) {
	t.Helper()
	w := beginWrite(t, s)
	artID, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactTaskGroup, Name: "tasks",
		SourcePath: "/roots/claude/tasks/x",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range sessionIDs {
		resolved, err := w.LinkArtifact(artID, canon.ArtifactLink{
			Agent: "claude-code", SessionExternalID: id,
			Relation: canon.LinkProducedBy, Evidence: canon.EvidenceIDMatch,
		})
		if err != nil {
			t.Fatal(err)
		}
		if resolved {
			t.Fatalf("link to %q resolved against a store without it", id)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
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
		Seq: 0, ExternalID: "entry-uuid-1", ContentID: "msg_abc",
		Role:      canon.RoleAssistant,
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

	// A resumed session repeats the same assistant content (same content
	// id + request id, different entry uuid): transcript row lands, usage
	// must not double-count.
	resumedID, err := w.UpsertSession(testSession("sess-b"), "h")
	if err != nil {
		t.Fatal(err)
	}
	repeated := msg
	repeated.ExternalID = "entry-uuid-2"
	if err := w.InsertMessage(resumedID, "claude-code", repeated); err != nil {
		t.Fatal(err)
	}

	// Different content + request id → separate usage.
	fresh := msg
	fresh.Seq = 1
	fresh.ExternalID = "entry-uuid-3"
	fresh.ContentID = "msg_def"
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

func TestUsageDedupeLegacyAndMostCompleteSnapshot(t *testing.T) {
	s, _ := openTemp(t)
	w := beginWrite(t, s)
	firstID, err := w.UpsertSession(testSession("first"), "h")
	if err != nil {
		t.Fatal(err)
	}
	reported := 0.42
	first := canon.Message{
		Seq: 0, ContentID: "legacy-msg", Role: canon.RoleAssistant,
		Usage: &canon.Usage{InputTokens: 10, OutputTokens: 2, ReportedCostUSD: &reported},
	}
	if err := w.InsertMessage(firstID, "claude-code", first); err != nil {
		t.Fatal(err)
	}
	secondID, err := w.UpsertSession(testSession("resumed"), "h")
	if err != nil {
		t.Fatal(err)
	}
	later := first
	later.Usage = &canon.Usage{InputTokens: 10, OutputTokens: 8}
	if err := w.InsertMessage(secondID, "claude-code", later); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM message_usage`); n != 1 {
		t.Fatalf("usage rows = %d, want 1", n)
	}
	var owner, output, reportedNanos int64
	var retainedCost float64
	if err := s.db.QueryRow(`
		SELECT m.session_id, u.output_tokens, u.reported_cost_usd, u.reported_cost_nanos
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id`).Scan(&owner, &output, &retainedCost, &reportedNanos); err != nil {
		t.Fatal(err)
	}
	if owner != firstID || output != 8 {
		t.Errorf("owner/output = %d/%d, want first session %d and final output 8",
			owner, output, firstID)
	}
	if retainedCost != reported || reportedNanos != 420_000_000 {
		t.Errorf("reported cost = %v / %d nanos, want retained %v", retainedCost, reportedNanos, reported)
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
	resolvedN, remaining, err := s.ResolvePending(context.Background(), true)
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

	resolvedN, remaining, err := s.ResolvePending(context.Background(), true)
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
	}, "/r/history.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertHistory(canon.HistoryEntry{
		Agent: "claude-code", Display: "orphan prompt", Timestamp: time.Now(),
	}, "/r/history.jsonl"); err != nil {
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

// --prune removes what a vanished source contributed. Every table that
// carries source provenance must be covered: history rows are only ever
// cleared by re-parsing their file, so a deleted history.jsonl used to
// leave every command in the index permanently, which is the opposite of
// what the flag promises.
func TestPruneMissingSourcesRemovesEverySourceOwnedRow(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	const (
		sessionSrc  = "/roots/claude/projects/x/gone.jsonl"
		artifactSrc = "/roots/claude/plans/gone.md"
		historySrc  = "/roots/claude/history.jsonl"
		keptSrc     = "/roots/claude/projects/x/kept.jsonl"
	)

	w := beginWrite(t, s)
	gone := testSession("gone")
	gone.SourcePath = sessionSrc
	goneID, err := w.UpsertSession(gone, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertMessage(goneID, "claude-code", canon.Message{
		Seq: 0, Role: canon.RoleUser, Content: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertSearchDoc(goneID, 0, "message", 0, "", "vanished text"); err != nil {
		t.Fatal(err)
	}
	if err := w.InsertToolCall(goneID, canon.ToolCall{Seq: 0, Name: "Bash"}); err != nil {
		t.Fatal(err)
	}

	kept := testSession("kept")
	kept.SourcePath = keptSrc
	keptID, err := w.UpsertSession(kept, "h2")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertSearchDoc(keptID, 0, "message", 0, "", "surviving text"); err != nil {
		t.Fatal(err)
	}

	artID, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "gone.md",
		Content: "plan body", SourcePath: artifactSrc,
	}, "h3")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertSearchDoc(0, artID, "plan", 0, "gone.md", "plan body"); err != nil {
		t.Fatal(err)
	}

	if err := w.InsertHistory(canon.HistoryEntry{
		Agent: "claude-code", Display: "rm -rf /", Timestamp: time.Now(),
	}, historySrc); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{sessionSrc, artifactSrc, historySrc, keptSrc} {
		if err := w.RecordSourceFile(p, "claude-code", "h", "stat", "", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	// Everything but keptSrc has disappeared from disk.
	exists := func(p string) bool { return p == keptSrc }
	n, err := s.PruneMissingSources(ctx, exists)
	if err != nil {
		t.Fatalf("PruneMissingSources: %v", err)
	}
	if n != 3 {
		t.Errorf("pruned %d sources, want 3", n)
	}

	checks := []struct {
		what  string
		query string
		want  int
	}{
		{"sessions", `SELECT COUNT(*) FROM sessions WHERE external_id = 'gone'`, 0},
		{"messages", `SELECT COUNT(*) FROM messages`, 0},
		{"tool calls", `SELECT COUNT(*) FROM tool_calls`, 0},
		{"artifacts", `SELECT COUNT(*) FROM artifacts`, 0},
		{"history", `SELECT COUNT(*) FROM history`, 0},
		{"source files", `SELECT COUNT(*) FROM source_files`, 1},
		{"surviving session", `SELECT COUNT(*) FROM sessions WHERE external_id = 'kept'`, 1},
		{"surviving search doc", `SELECT COUNT(*) FROM search_docs`, 1},
	}
	for _, c := range checks {
		if got := count(t, s, c.query); got != c.want {
			t.Errorf("%s = %d, want %d", c.what, got, c.want)
		}
	}

	// The FTS index must lose the pruned documents with them.
	if got := count(t, s, `SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'vanished'`); got != 0 {
		t.Errorf("pruned text still searchable (%d hits)", got)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM search_fts WHERE search_fts MATCH 'surviving'`); got != 1 {
		t.Errorf("surviving text lost from the index (%d hits)", got)
	}
}

// Rows imported from a v1 database have no live source on disk — that
// retention is the whole point of the import — so prune must leave them.
func TestPruneKeepsImportedRows(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	w := beginWrite(t, s)
	imported := testSession("imported")
	imported.Origin = canon.OriginImportedV1
	imported.SourcePath = "/roots/claude/projects/x/legacy.jsonl"
	if _, err := w.UpsertSession(imported, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.RecordSourceFile(imported.SourcePath, "claude-code", "h", "stat", "", 1); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PruneMissingSources(ctx, func(string) bool { return false }); err != nil {
		t.Fatalf("PruneMissingSources: %v", err)
	}
	if got := count(t, s, `SELECT COUNT(*) FROM sessions WHERE external_id = 'imported'`); got != 1 {
		t.Errorf("imported session was pruned (%d rows left)", got)
	}
}

// An artifact re-emitted with EMPTY content must replace what is stored —
// content, metadata, and search document alike. This is the store half of
// the emptied-sidecar fix: the adapter emits the empty state (a todo list
// TodoWrite has cleared), and the upsert is what has to make it stick
// rather than leaving the last populated version to be served forever.
func TestEmptyArtifactContentReplacesStoredContent(t *testing.T) {
	s, _ := openTemp(t)
	const name = "sess-a-agent-xyz.json"

	w := beginWrite(t, s)
	id, _, err := w.WriteArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactTodoList, Name: name,
		Content:  "ship the parser\nwrite the tests",
		Metadata: []byte(`[{"content":"ship the parser"}]`),
	}, "h1")
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM search_docs WHERE artifact_id = ?`, id); n != 1 {
		t.Fatalf("search docs = %d, want the populated list indexed", n)
	}

	w = beginWrite(t, s)
	id2, _, err := w.WriteArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactTodoList, Name: name,
		Content: "", Metadata: []byte(`[]`),
	}, "h2")
	if err != nil {
		t.Fatalf("emptied write: %v", err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if id2 != id {
		t.Fatalf("row id changed on emptied upsert: %d → %d", id, id2)
	}

	// Still in the archive, now reading as the empty list it is.
	if n := count(t, s, `SELECT COUNT(*) FROM artifacts`); n != 1 {
		t.Fatalf("artifacts = %d, want the row kept", n)
	}
	var content, meta string
	if err := s.db.QueryRow(
		`SELECT content, metadata_json FROM artifacts WHERE id = ?`, id,
	).
		Scan(&content, &meta); err != nil {
		t.Fatal(err)
	}
	if content != "" || meta != `[]` {
		t.Errorf("stored content=%q metadata=%q, want the emptied state", content, meta)
	}
	// The stale text must leave the index too, or search keeps answering
	// with items the agent already finished.
	if n := count(t, s, `SELECT COUNT(*) FROM search_docs WHERE artifact_id = ?`, id); n != 0 {
		t.Errorf("search docs = %d, want the stale document cleared", n)
	}
}

// A parked link waiting on a session that is simply not indexed YET must
// survive until it resolves. One pointing at something that will never
// exist must not survive forever: it grew the table without bound and
// reported unresolved_links as a live signal when it was noise.
func TestPendingLinksAgeOutButLateArrivalsStillResolve(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	// One link will never resolve; one is waiting on a session that
	// arrives a few passes later.
	parkArtifactLink(t, s, "never-exists", "arrives-late")
	if n := count(t, s, `SELECT COUNT(*) FROM pending_artifact_links`); n != 2 {
		t.Fatalf("parked links = %d, want 2", n)
	}

	// Two passes: both links are still parked, and the late session has
	// not arrived yet.
	for i := range 2 {
		if _, remaining, err := s.ResolvePending(ctx, true); err != nil {
			t.Fatal(err)
		} else if remaining != 2 {
			t.Fatalf("after pass %d remaining = %d, want 2", i+1, remaining)
		}
	}

	// The session finally lands — its link must still resolve.
	w := beginWrite(t, s)
	late := testSession("arrives-late")
	if _, err := w.UpsertSession(late, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	resolved, remaining, err := s.ResolvePending(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Errorf("resolved = %d, want 1 (the late arrival)", resolved)
	}
	if remaining != 1 {
		t.Errorf("remaining = %d, want 1 (the impossible link, still ageing)", remaining)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM artifact_sessions`); n != 1 {
		t.Errorf("artifact_sessions = %d, want 1", n)
	}

	// Keep going: the impossible link is dropped once it passes the limit.
	for range pendingLinkAttemptLimit + 1 {
		if _, _, err := s.ResolvePending(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pending_artifact_links`); n != 0 {
		t.Errorf("unresolvable link survived %d passes (%d rows left)", pendingLinkAttemptLimit+1, n)
	}
	// The resolved link is untouched by the ageing.
	if n := count(t, s, `SELECT COUNT(*) FROM artifact_sessions`); n != 1 {
		t.Errorf("artifact_sessions = %d after ageing, want 1", n)
	}
}

// Passes that ingested nothing must not spend a parked link's attempts.
// Watch mode fires a pass per debounce — a formatter touching an unrelated
// file, an editor writing a swap file — and ageing on every one of them
// made "five attempts" mean minutes of wall clock, dropping links whose
// endpoint was merely late (a session restored from a backup an hour on).
func TestNoChangeRunsDoNotAgePendingLinks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	parkArtifactLink(t, s, "arrives-much-later")

	attempts := func() int {
		return count(t, s, `SELECT COALESCE(MAX(attempts), -1) FROM pending_artifact_links`)
	}
	if got := attempts(); got != 0 {
		t.Fatalf("attempts = %d on a freshly parked link, want 0", got)
	}

	// Far more no-change passes than the limit allows.
	for range pendingLinkAttemptLimit * 3 {
		if _, remaining, err := s.ResolvePending(ctx, false); err != nil {
			t.Fatal(err)
		} else if remaining != 1 {
			t.Fatalf("remaining = %d, want the link still parked", remaining)
		}
	}
	if got := attempts(); got != 0 {
		t.Fatalf("attempts = %d after %d no-change passes, want 0",
			got, pendingLinkAttemptLimit*3)
	}

	// The session finally lands — the link is still there to resolve.
	w := beginWrite(t, s)
	if _, err := w.UpsertSession(testSession("arrives-much-later"), "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if resolved, _, err := s.ResolvePending(ctx, true); err != nil {
		t.Fatal(err)
	} else if resolved != 1 {
		t.Fatalf("resolved = %d, want the late arrival linked", resolved)
	}
}

// Runs that DID change something still age a link out at the limit — the
// no-change exemption must not turn the limit off.
func TestChangingRunsStillAgePendingLinks(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	parkArtifactLink(t, s, "never-exists")

	// Interleaving no-change passes must not shorten NOR extend the count:
	// only the ageing ones advance it.
	for range pendingLinkAttemptLimit {
		if _, _, err := s.ResolvePending(ctx, false); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.ResolvePending(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pending_artifact_links`); n != 1 {
		t.Fatalf("link dropped after exactly the limit (%d rows), want it to survive", n)
	}
	if _, _, err := s.ResolvePending(ctx, true); err != nil {
		t.Fatal(err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pending_artifact_links`); n != 0 {
		t.Errorf("unresolvable link survived past the limit (%d rows left)", n)
	}
}

// The same ageing applies to session→session relations.
func TestPendingRelationsAgeOut(t *testing.T) {
	ctx := context.Background()
	s, _ := openTemp(t)

	w := beginWrite(t, s)
	if _, err := w.UpsertSession(testSession("from"), "h"); err != nil {
		t.Fatal(err)
	}
	resolved, err := w.AddSessionRelation(canon.SessionRelation{
		Agent: "claude-code", FromExternalID: "from",
		ToExternalID: "a-session-that-never-was", Kind: canon.RelForkOf,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved {
		t.Fatal("relation resolved against a missing endpoint")
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	for range pendingLinkAttemptLimit + 1 {
		if _, _, err := s.ResolvePending(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, s, `SELECT COUNT(*) FROM pending_relations`); n != 0 {
		t.Errorf("unresolvable relation survived (%d rows left)", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM session_relations`); n != 0 {
		t.Errorf("a phantom relation was materialised (%d rows)", n)
	}
}
