package query

import (
	"context"
	"path/filepath"
	"testing"

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
