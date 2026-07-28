package opencode

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func fixtureRoot(t *testing.T) agent.Root {
	t.Helper()
	p, err := filepath.Abs("../../../testdata/agents/opencode")
	if err != nil {
		t.Fatal(err)
	}
	return agent.Root{Agent: Slug, Path: p, Origin: agent.RootFromDefault}
}

// One source per session, not two. Parse reads the whole session from the
// document either way, so discovering the message directory separately
// parsed and counted every record twice; it rides along as a companion
// path instead, which keeps change detection without the second parse.
func TestDiscoverEmitsOneSourcePerSession(t *testing.T) {
	root := fixtureRoot(t)
	refs, err := New().Discover(context.Background(), root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("sources = %d, want 1 per session", len(refs))
	}
	ref := refs[0]
	if ref.Kind != agent.SourceFile {
		t.Errorf("kind = %s, want the session document", ref.Kind)
	}
	if want := filepath.Join(root.Path, "storage", "session", "proj9hash", "ses_oc001.json"); ref.Path != want {
		t.Errorf("path = %q, want %q", ref.Path, want)
	}
	want := []string{filepath.Join(root.Path, "storage", "message", "ses_oc001")}
	if !slices.Equal(ref.CompanionPaths, want) {
		t.Errorf("companions = %v, want %v", ref.CompanionPaths, want)
	}
}

func TestParseSessionDocument(t *testing.T) {
	root := fixtureRoot(t)
	sink := &agenttest.Sink{}
	src := agent.SourceRef{
		Root: root,
		Path: filepath.Join(root.Path, "storage", "session", "proj9hash", "ses_oc001.json"),
		Kind: agent.SourceFile,
	}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(sink.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(sink.Sessions))
	}
	sess := sink.Sessions[0]
	if sess.ExternalID != "ses_oc001" || sess.Title != "Refactor auth middleware" {
		t.Errorf("session = %+v", sess)
	}
	if sess.CWD != "/home/u/demo/api" {
		t.Errorf("cwd = %q (must come from the session document)", sess.CWD)
	}
	if sess.CreatedAt.IsZero() || !sess.ModifiedAt.After(sess.CreatedAt) {
		t.Errorf("times = %v / %v", sess.CreatedAt, sess.ModifiedAt)
	}

	if len(sink.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(sink.Messages))
	}
	asst := sink.Messages[1]
	if asst.Role != canon.RoleAssistant || asst.Model != "claude-sonnet-5" {
		t.Errorf("assistant = role %s model %s", asst.Role, asst.Model)
	}
	if asst.Usage == nil {
		t.Fatal("assistant without usage")
	}
	u := asst.Usage
	// OpenCode reasoning (90) is additive to output (510); the adapter
	// folds it into billable output per the canon.Usage contract.
	if u.InputTokens != 2400 || u.OutputTokens != 600 ||
		u.CacheReadTokens != 11000 || u.CacheWriteTokens != 800 {
		t.Errorf("usage = %+v", u)
	}
	if u.ReasoningTokens != 90 {
		t.Errorf("reasoning tokens = %d, want 90 (kept as detail)", u.ReasoningTokens)
	}
	if u.ReportedCostUSD == nil || *u.ReportedCostUSD != 0.0142 {
		t.Errorf("reported cost = %v", u.ReportedCostUSD)
	}

	if len(sink.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(sink.ToolCalls))
	}
	tc := sink.ToolCalls[0]
	if tc.Name != "edit" || tc.Kind != canon.ToolFileEdit ||
		tc.ResultStatus != "ok" || tc.FilePath != "internal/auth/middleware.go" {
		t.Errorf("tool call = %+v", tc)
	}
}

// A session whose message directory does not exist yet is normal, and the
// companion must not make Discover skip it or Parse fail.
func TestSessionWithoutMessagesStillDiscovers(t *testing.T) {
	root := agent.Root{Agent: Slug, Path: t.TempDir(), Origin: agent.RootFromDefault}
	docDir := filepath.Join(root.Path, "storage", "session", "proj")
	if err := os.MkdirAll(docDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := `{"id":"ses_x","title":"t","time":{"created":1,"updated":2},"directory":"/w"}`
	if err := os.WriteFile(filepath.Join(docDir, "ses_x.json"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	refs, err := New().Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("sources = %d, want 1", len(refs))
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), refs[0], sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sink.Sessions) != 1 || len(sink.Messages) != 0 {
		t.Errorf("parse produced %d sessions / %d messages, want 1/0",
			len(sink.Sessions), len(sink.Messages))
	}
}
