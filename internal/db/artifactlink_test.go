package db

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// TestLinkPlanArtifacts proves plans link to the session whose
// ExitPlanMode call carries the same plan text — despite whitespace
// differences — and that non-matching plans stay unlinked and repeat
// passes are no-ops.
func TestLinkPlanArtifacts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	w, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessA, err := w.UpsertSession(canon.Session{Agent: "claude-code", ExternalID: "sess-a"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	sessB, err := w.UpsertSession(canon.Session{Agent: "claude-code", ExternalID: "sess-b"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	// Session A approved the matching plan; session B approved another.
	for _, tc := range []struct {
		sess  int64
		seq   int
		input string
	}{
		{sessA, 0, `{"plan":"# Ship it\n\n1. Build\n2. Test"}`},
		{sessB, 0, `{"plan":"# Different plan entirely"}`},
	} {
		if err := w.InsertToolCall(tc.sess, canon.ToolCall{
			MessageSeq: tc.seq, Seq: tc.seq, Name: "ExitPlanMode",
			Kind: canon.ToolOther, Input: []byte(tc.input),
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The file on disk carries trailing whitespace and a final newline the
	// tool input lacks.
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "ship-it.md",
		Content: "# Ship it  \n\n1. Build\n2. Test\n",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "orphan.md",
		Content: "# A plan no call ever carried",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	linked, err := s.LinkPlanArtifacts(ctx)
	if err != nil {
		t.Fatalf("LinkPlanArtifacts: %v", err)
	}
	if linked != 1 {
		t.Fatalf("linked = %d, want 1", linked)
	}
	var ext, evidence string
	if err := s.db.QueryRowContext(ctx, `
		SELECT se.external_id, ass.evidence
		FROM artifact_sessions ass
		JOIN artifacts a ON a.id = ass.artifact_id
		JOIN sessions se ON se.id = ass.session_id
		WHERE a.name = 'ship-it.md'`).Scan(&ext, &evidence); err != nil {
		t.Fatal(err)
	}
	if ext != "sess-a" || evidence != "content_ref" {
		t.Errorf("link = %s/%s, want sess-a/content_ref", ext, evidence)
	}
	var orphanLinks int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_sessions ass
		JOIN artifacts a ON a.id = ass.artifact_id
		WHERE a.name = 'orphan.md'`).Scan(&orphanLinks); err != nil {
		t.Fatal(err)
	}
	if orphanLinks != 0 {
		t.Errorf("orphan plan links = %d, want 0", orphanLinks)
	}

	// Second pass: nothing new (linked plans are skipped, the orphan still
	// matches nothing).
	linked, err = s.LinkPlanArtifacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if linked != 0 {
		t.Errorf("second pass linked = %d, want 0", linked)
	}
}

// TestLinkMemoryArtifacts proves memories link to the sessions whose
// file_write/file_edit calls targeted their on-disk path, that memories
// nobody wrote stay unlinked, and that repeat passes are no-ops.
func TestLinkMemoryArtifacts(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	w, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{Agent: "claude-code", ExternalID: "sess-mem"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []canon.ToolCall{
		{
			Name: "Write", Kind: canon.ToolFileWrite,
			FilePath: "/home/u/.claude/projects/-home-u-app/memory/MEMORY.md",
		},
		{Name: "Read", Kind: canon.ToolFileRead, // reads are not provenance
			FilePath: "/home/u/.claude/projects/-home-u-app/memory/other.md"},
	} {
		tc.MessageSeq, tc.Seq = i, i
		if err := w.InsertToolCall(sessID, tc); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"-home-u-app/MEMORY.md", "-home-u-app/other.md"} {
		if _, err := w.UpsertArtifact(canon.Artifact{
			Agent: "claude-code", Kind: canon.ArtifactMemory, Name: name,
			Content: "# notes",
		}, "h"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	linked, err := s.LinkMemoryArtifacts(ctx)
	if err != nil {
		t.Fatalf("LinkMemoryArtifacts: %v", err)
	}
	if linked != 1 {
		t.Fatalf("linked = %d, want 1 (write links, read does not)", linked)
	}
	var ext, evidence string
	if err := s.db.QueryRowContext(ctx, `
		SELECT se.external_id, ass.evidence
		FROM artifact_sessions ass
		JOIN artifacts a ON a.id = ass.artifact_id
		JOIN sessions se ON se.id = ass.session_id
		WHERE a.name = '-home-u-app/MEMORY.md'`).Scan(&ext, &evidence); err != nil {
		t.Fatal(err)
	}
	if ext != "sess-mem" || evidence != "content_ref" {
		t.Errorf("link = %s/%s, want sess-mem/content_ref", ext, evidence)
	}

	linked, err = s.LinkMemoryArtifacts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if linked != 0 {
		t.Errorf("second pass linked = %d, want 0", linked)
	}
}
