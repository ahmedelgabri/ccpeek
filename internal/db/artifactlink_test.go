package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// The rule engine is agent-neutral, so these tests declare their own rules
// rather than importing an adapter's — which is also what keeps them from
// silently passing on a Claude-shaped assumption baked into the store.
func planRule() canon.LinkRule {
	norm := func(s string) (string, bool) {
		if strings.TrimSpace(s) == "" {
			return "", false
		}
		lines := strings.Split(strings.TrimSpace(s), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t\r")
		}
		return strings.Join(lines, "\n"), true
	}
	return canon.LinkRule{
		Kind:        canon.ArtifactPlan,
		Calls:       canon.ToolCallSelector{ToolName: "ExitPlanMode"},
		ArtifactKey: func(a canon.LinkArtifact) (string, bool) { return norm(a.Content) },
		CallKey: func(c canon.LinkToolCall) (string, bool) {
			var in struct {
				Plan string `json:"plan"`
			}
			if json.Unmarshal([]byte(c.InputJSON), &in) != nil {
				return "", false
			}
			return norm(in.Plan)
		},
	}
}

func memoryRule() canon.LinkRule {
	tail := func(path string) (string, bool) {
		i := strings.LastIndex(path, "/projects/")
		if i < 0 || !strings.Contains(path[i:], "/memory/") {
			return "", false
		}
		return path[i:], true
	}
	return canon.LinkRule{
		Kind: canon.ArtifactMemory,
		Calls: canon.ToolCallSelector{
			Kinds:            []canon.ToolKind{canon.ToolFileWrite, canon.ToolFileEdit},
			FilePathContains: "/memory/",
		},
		ArtifactKey: func(a canon.LinkArtifact) (string, bool) {
			dir, base, found := strings.Cut(a.Name, "/")
			if !found || dir == "" || base == "" {
				return "", false
			}
			return "/projects/" + dir + "/memory/" + base, true
		},
		CallKey: func(c canon.LinkToolCall) (string, bool) { return tail(c.FilePath) },
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func linkPlans(ctx context.Context, s *Store) (int, int, error) {
	return s.ResolveArtifactLinks(ctx, []canon.LinkRule{planRule()})
}

func linkMemories(ctx context.Context, s *Store) (int, int, error) {
	return s.ResolveArtifactLinks(ctx, []canon.LinkRule{memoryRule()})
}

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

	linked, _, err := linkPlans(ctx, s)
	if err != nil {
		t.Fatalf("resolving plan links: %v", err)
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
	linked, _, err = linkPlans(ctx, s)
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

	linked, _, err := linkMemories(ctx, s)
	if err != nil {
		t.Fatalf("resolving memory links: %v", err)
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

	linked, _, err = linkMemories(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if linked != 0 {
		t.Errorf("second pass linked = %d, want 0", linked)
	}
}

// TestLaterSessionLinksToExistingPlan: links are N:M — a session that
// approves an already-linked plan LATER must still gain its link (the
// old "only unlinked artifacts" filter skipped it forever).
func TestLaterSessionLinksToExistingPlan(t *testing.T) {
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
	if err := w.InsertToolCall(sessA, canon.ToolCall{
		Name: "ExitPlanMode", Kind: canon.ToolOther,
		Input: []byte(`{"plan":"# Shared plan"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "shared.md",
		Content: "# Shared plan",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, _, err := linkPlans(ctx, s); err != nil || n != 1 {
		t.Fatalf("first pass linked = %d (err %v), want 1", n, err)
	}

	// A second session approves the same plan after the artifact was
	// already linked.
	w, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessB, err := w.UpsertSession(canon.Session{Agent: "claude-code", ExternalID: "sess-b"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertToolCall(sessB, canon.ToolCall{
		Name: "ExitPlanMode", Kind: canon.ToolOther,
		Input: []byte(`{"plan":"# Shared plan"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if n, _, err := linkPlans(ctx, s); err != nil || n != 1 {
		t.Fatalf("second pass linked = %d (err %v), want 1 (the new pair)", n, err)
	}
	var links int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_sessions ass
		JOIN artifacts a ON a.id = ass.artifact_id
		WHERE a.name = 'shared.md'`).Scan(&links); err != nil {
		t.Fatal(err)
	}
	if links != 2 {
		t.Errorf("plan links = %d, want 2 (both approving sessions)", links)
	}
}

// TestLinkPlanArtifactsReconcilesStaleEvidence: a plan REWRITTEN under
// the same natural key must lose the content-derived link whose text no
// longer matches — while links owned by other evidence survive.
func TestLinkPlanArtifactsReconcilesStaleEvidence(t *testing.T) {
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
	if err := w.InsertToolCall(sessA, canon.ToolCall{
		Name: "ExitPlanMode", Kind: canon.ToolOther,
		Input: []byte(`{"plan":"# Version one"}`),
	}); err != nil {
		t.Fatal(err)
	}
	planID, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "plan.md",
		Content: "# Version one\n",
	}, "h1")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if added, removed, err := linkPlans(ctx, s); err != nil || added != 1 || removed != 0 {
		t.Fatalf("first pass = +%d -%d (%v), want +1 -0", added, removed, err)
	}

	// A link owned by DIFFERENT evidence on the same pair's artifact: the
	// reconciler must never touch it.
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO artifact_sessions (artifact_id, session_id, relation, evidence)
		VALUES (?, ?, 'applies_to', 'id_match')`, planID, sessA); err != nil {
		t.Fatal(err)
	}

	// The plan file is rewritten under the same name with unrelated text.
	w, err = s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactPlan, Name: "plan.md",
		Content: "# Rewritten: nothing like version one\n",
	}, "h2"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	if added, removed, err := linkPlans(ctx, s); err != nil || added != 0 || removed != 1 {
		t.Fatalf("reconcile pass = +%d -%d (%v), want +0 -1 (stale evidence)", added, removed, err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_sessions
		WHERE artifact_id = ? AND evidence = 'content_ref'`, planID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("stale content_ref links = %d, want 0", n)
	}
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM artifact_sessions
		WHERE artifact_id = ? AND evidence = 'id_match'`, planID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("non-resolver link count = %d, want 1 (must be preserved)", n)
	}
}

// The whole point of moving the rules out of the store: an agent that is
// not Claude Code, with its own tool name and its own artifact shape, gets
// its artifacts linked without a line of code in internal/db.
//
// This rule shares nothing with Claude's — a different tool name, a
// different key derivation, a different artifact kind.
func TestRuleEngineServesAnyAgentsShape(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	w, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "pi", ExternalID: "sess-invented",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	// A made-up agent convention: a "publish_note" call whose input names
	// the note it wrote, and note artifacts named "notes/<slug>".
	if err := w.InsertToolCall(sessID, canon.ToolCall{
		Seq: 0, MessageSeq: 4, Name: "publish_note", Kind: canon.ToolOther,
		Input: []byte(`{"slug":"weekly-summary"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.UpsertArtifact(canon.Artifact{
		Agent: "pi", Kind: canon.ArtifactPlan, Name: "notes/weekly-summary",
	}, "h"); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	rule := canon.LinkRule{
		Kind:  canon.ArtifactPlan,
		Calls: canon.ToolCallSelector{ToolName: "publish_note"},
		ArtifactKey: func(a canon.LinkArtifact) (string, bool) {
			return strings.CutPrefix(a.Name, "notes/")
		},
		CallKey: func(c canon.LinkToolCall) (string, bool) {
			var in struct {
				Slug string `json:"slug"`
			}
			if json.Unmarshal([]byte(c.InputJSON), &in) != nil || in.Slug == "" {
				return "", false
			}
			return in.Slug, true
		},
	}

	added, _, err := s.ResolveArtifactLinks(ctx, []canon.LinkRule{rule})
	if err != nil {
		t.Fatalf("ResolveArtifactLinks: %v", err)
	}
	if added != 1 {
		t.Fatalf("links added = %d, want 1", added)
	}

	var anchor sql.NullInt64
	if err := s.ReadDB().QueryRowContext(ctx,
		`SELECT anchor_seq FROM artifact_sessions`).Scan(&anchor); err != nil {
		t.Fatal(err)
	}
	if !anchor.Valid || anchor.Int64 != 4 {
		t.Errorf("anchor = %v, want the publish_note call's message seq (4)", anchor)
	}
}

// An anchor-only rule records where an artifact was produced without
// creating or removing the link — the link belongs to other evidence.
func TestAnchorOnlyRuleLeavesTheLinkAlone(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	w, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "sess-anchor-only",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	for _, seq := range []int{2, 7} {
		if err := w.InsertToolCall(sessID, canon.ToolCall{
			Seq: seq, MessageSeq: seq, Name: "TodoWrite", Kind: canon.ToolOther,
		}); err != nil {
			t.Fatal(err)
		}
	}
	artID, err := w.UpsertArtifact(canon.Artifact{
		Agent: "claude-code", Kind: canon.ArtifactTodoList, Name: "todo.json",
	}, "h")
	if err != nil {
		t.Fatal(err)
	}
	// The link exists on OTHER evidence — the session uuid in the name.
	if _, err := w.LinkArtifact(artID, canon.ArtifactLink{
		Agent: "claude-code", ArtifactKind: canon.ArtifactTodoList,
		ArtifactName: "todo.json", SessionExternalID: "sess-anchor-only",
		Relation: canon.LinkProducedBy, Evidence: canon.EvidenceFilenameUUID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	rule := canon.LinkRule{
		Kind:  canon.ArtifactTodoList,
		Calls: canon.ToolCallSelector{ToolName: "TodoWrite"},
	}
	if !rule.Anchors() {
		t.Fatal("a rule with no key functions must be anchor-only")
	}
	added, removed, err := s.ResolveArtifactLinks(ctx, []canon.LinkRule{rule})
	if err != nil {
		t.Fatalf("ResolveArtifactLinks: %v", err)
	}
	if added != 0 || removed != 0 {
		t.Errorf("anchor-only rule changed links: +%d -%d", added, removed)
	}

	var evidence string
	var anchor sql.NullInt64
	if err := s.ReadDB().QueryRowContext(ctx,
		`SELECT evidence, anchor_seq FROM artifact_sessions`).Scan(&evidence, &anchor); err != nil {
		t.Fatal(err)
	}
	if evidence != string(canon.EvidenceFilenameUUID) {
		t.Errorf("evidence = %q, want the original %q", evidence, canon.EvidenceFilenameUUID)
	}
	if !anchor.Valid || anchor.Int64 != 7 {
		t.Errorf("anchor = %v, want 7 (the LAST TodoWrite)", anchor)
	}
}
