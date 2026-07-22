package query

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// TestArtifactSessionAnchors proves the artifact detail resolves the
// transcript seq of the tool call that produced a kind (TodoWrite for a
// todo list), and leaves kinds with no producer unanchored.
func TestArtifactSessionAnchors(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "sess-todo",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	// Two TodoWrite calls; the anchor must be the LAST one (seq order).
	for _, tc := range []canon.ToolCall{
		{SessionExternalID: "sess-todo", MessageSeq: 4, Seq: 0, Name: "TodoWrite", Kind: canon.ToolOther},
		{SessionExternalID: "sess-todo", MessageSeq: 9, Seq: 1, Name: "TodoWrite", Kind: canon.ToolOther},
		{SessionExternalID: "sess-todo", MessageSeq: 6, Seq: 2, Name: "Read", Kind: canon.ToolFileRead},
	} {
		if err := w.InsertToolCall(sessID, tc); err != nil {
			t.Fatal(err)
		}
	}
	todoID, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactTodoList, Name: "sess-todo-agent.json",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.LinkArtifact(todoID, canon.ArtifactLink{
		Agent: "claude-code", ArtifactKind: canon.ArtifactTodoList,
		ArtifactName: "sess-todo-agent.json", SessionExternalID: "sess-todo",
		Relation: canon.LinkProducedBy, Evidence: canon.EvidenceFilenameUUID,
	}); err != nil {
		t.Fatal(err)
	}
	// A memory artifact linked to the same session: no producer tool, so
	// no anchor even though the session has tool calls.
	memID, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactMemory, Name: "proj/MEMORY.md",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.LinkArtifact(memID, canon.ArtifactLink{
		Agent: "claude-code", ArtifactKind: canon.ArtifactMemory,
		ArtifactName: "proj/MEMORY.md", SessionExternalID: "sess-todo",
		Relation: canon.LinkAppliesTo, Evidence: canon.EvidenceCWDMatch,
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	svc := New(store, table)

	todo, err := svc.Artifact(ctx, "claude-code", "todo_list", "sess-todo-agent.json", nil)
	if err != nil {
		t.Fatalf("Artifact(todo): %v", err)
	}
	if got := todo.SessionAnchors["sess-todo"]; got != 9 {
		t.Errorf("todo anchor = %d, want 9 (last TodoWrite's message seq)", got)
	}

	mem, err := svc.Artifact(ctx, "claude-code", "memory", "proj/MEMORY.md", nil)
	if err != nil {
		t.Fatalf("Artifact(memory): %v", err)
	}
	if len(mem.SessionAnchors) != 0 {
		t.Errorf("memory anchors = %v, want none (no producing tool call)", mem.SessionAnchors)
	}
	if len(mem.SessionIDs) != 1 {
		t.Errorf("memory sessionIds = %v, want the linked session", mem.SessionIDs)
	}
}

// TestPlanAnchorMatchesItsOwnCall: a session holding several plans must
// anchor each plan artifact to the ExitPlanMode call that carried ITS
// text, not the session's last one.
func TestPlanAnchorMatchesItsOwnCall(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "sess-plans",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		msgSeq, seq int
		plan        string
	}{
		{3, 0, `{"plan":"# First plan"}`},
		{12, 1, `{"plan":"# Second plan"}`},
	} {
		if err := w.InsertToolCall(sessID, canon.ToolCall{
			MessageSeq: tc.msgSeq, Seq: tc.seq, Name: "ExitPlanMode",
			Kind: canon.ToolOther, Input: []byte(tc.plan),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "first.md",
		Content: "# First plan\n",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LinkPlanArtifacts(ctx); err != nil {
		t.Fatal(err)
	}

	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	plan, err := New(store, table).Artifact(ctx, "claude-code", "plan", "first.md", nil)
	if err != nil {
		t.Fatalf("Artifact(plan): %v", err)
	}
	if got := plan.SessionAnchors["sess-plans"]; got != 3 {
		t.Errorf("plan anchor = %d, want 3 (the call carrying this plan, not the last)", got)
	}
	if len(plan.SessionIDs) != 1 || plan.SessionIDs[0] != "sess-plans" {
		t.Errorf("plan sessionIds = %v", plan.SessionIDs)
	}
}

// TestMemoryAnchorPointsAtItsWrite: a memory anchors to the last write
// that targeted its path, not other memory writes in the same session.
func TestMemoryAnchorPointsAtItsWrite(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "sess-mem",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []canon.ToolCall{
		{
			MessageSeq: 2, Name: "Write", Kind: canon.ToolFileWrite,
			FilePath: "/h/.claude/projects/-p/memory/MEMORY.md",
		},
		{
			MessageSeq: 7, Name: "Edit", Kind: canon.ToolFileEdit,
			FilePath: "/h/.claude/projects/-p/memory/MEMORY.md",
		},
		{
			MessageSeq: 9, Name: "Write", Kind: canon.ToolFileWrite,
			FilePath: "/h/.claude/projects/-p/memory/unrelated.md",
		},
	} {
		tc.Seq = i
		if err := w.InsertToolCall(sessID, tc); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactMemory, Name: "-p/MEMORY.md",
		Content: "# notes",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.LinkMemoryArtifacts(ctx); err != nil {
		t.Fatal(err)
	}

	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	mem, err := New(store, table).Artifact(ctx, "claude-code", "memory", "-p/MEMORY.md", nil)
	if err != nil {
		t.Fatalf("Artifact(memory): %v", err)
	}
	if got := mem.SessionAnchors["sess-mem"]; got != 7 {
		t.Errorf("memory anchor = %d, want 7 (its own last edit, not the unrelated write)", got)
	}
}

// TestStatsScanFindingsExcludesIgnored: the overview tile must count
// only active findings — ignoring one removes it from the count.
func TestStatsScanFindingsExcludesIgnored(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for _, q := range []string{
		`INSERT INTO scan_findings (rule_id, description, entity_type, natural_key, match_redacted, line_number, scanned_at)
		 VALUES ('slack-token', '', 'message', 'message/sess-x', 'xoxb…', 3, '2026-07-13T00:00:00Z')`,
		`INSERT INTO scan_findings (rule_id, description, entity_type, natural_key, match_redacted, line_number, scanned_at)
		 VALUES ('aws-key', '', 'message', 'message/sess-x', 'AKIA…', 7, '2026-07-13T00:00:00Z')`,
		`INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
		 VALUES ('scan_finding', 'message/sess-x/slack-token/3', 'scan_ignore', '{}', '2026-07-13T00:00:00Z')`,
	} {
		if _, err := store.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	st, err := New(store, table).Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if st.ScanFindings != 1 {
		t.Errorf("scanFindings = %d, want 1 (the ignored finding must not count)", st.ScanFindings)
	}
}

// TestUsageSessionsAreDistinct: a session that used two models in one
// day occupies two rollup rows; the day group must still count it once.
func TestUsageSessionsAreDistinct(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "sess-two-models",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	for seq, model := range []string{"claude-sonnet-5", "claude-haiku-4-5"} {
		if err := w.InsertMessage(sessID, "claude-code", canon.Message{
			Seq: seq, Role: canon.RoleAssistant, Model: model,
			CreatedAt: mustTime(t, "2026-07-10T10:00:00Z"),
			Content:   []byte(`{}`),
			Usage:     &canon.Usage{InputTokens: 100, OutputTokens: 50},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegenerateRollups(ctx, table); err != nil {
		t.Fatal(err)
	}

	rows, err := New(store, table).Usage(ctx, UsageFilter{GroupBy: "day"})
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("day rows = %d, want 1", len(rows))
	}
	if rows[0].Sessions != 1 {
		t.Errorf("day sessions = %d, want 1 (one session across two models)", rows[0].Sessions)
	}
	if rows[0].Messages != 2 {
		t.Errorf("day messages = %d, want 2 (additive metrics unchanged)", rows[0].Messages)
	}
}

func mustTime(t *testing.T, s string) (out time.Time) {
	t.Helper()
	out, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
