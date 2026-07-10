package opencode

import (
	"context"
	"path/filepath"
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

func TestDiscover(t *testing.T) {
	refs, err := New().Discover(context.Background(), fixtureRoot(t))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// 1 session doc + 1 message dir.
	if len(refs) != 2 {
		t.Fatalf("sources = %d, want 2", len(refs))
	}
	kinds := map[agent.SourceKind]int{}
	for _, r := range refs {
		kinds[r.Kind]++
	}
	if kinds[agent.SourceFile] != 1 || kinds[agent.SourceDir] != 1 {
		t.Errorf("kinds = %v", kinds)
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
	if u.InputTokens != 2400 || u.OutputTokens != 510 ||
		u.CacheReadTokens != 11000 || u.CacheWriteTokens != 800 {
		t.Errorf("usage = %+v", u)
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

func TestParseMessageDirReemitsSession(t *testing.T) {
	root := fixtureRoot(t)
	sink := &agenttest.Sink{}
	src := agent.SourceRef{
		Root: root,
		Path: filepath.Join(root.Path, "storage", "message", "ses_oc001"),
		Kind: agent.SourceDir,
	}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(sink.Sessions) != 1 || len(sink.Messages) != 3 {
		t.Errorf("dir-triggered parse: %d sessions, %d messages, want 1/3",
			len(sink.Sessions), len(sink.Messages))
	}
}
