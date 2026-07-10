package cursor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

const sessionID = "e4f5a6b7-1111-2222-3333-cursor000001"

func parseFixture(t *testing.T) *agenttest.Sink {
	t.Helper()
	root, err := filepath.Abs("../../../testdata/agents/cursor")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := New().Discover(context.Background(),
		agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("sources = %d, want 1", len(refs))
	}
	if refs[0].Kind != agent.SourceDatabase {
		t.Fatalf("kind = %q, want database", refs[0].Kind)
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), refs[0], sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sink
}

func TestParseStoreDB(t *testing.T) {
	sink := parseFixture(t)

	if len(sink.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(sink.Sessions))
	}
	sess := sink.Sessions[0]
	if sess.ExternalID != sessionID {
		t.Errorf("external id = %q (must be the session-uuid dir)", sess.ExternalID)
	}
	if sess.Title != "Speed up the CI pipeline" {
		t.Errorf("title = %q (must come from hex meta)", sess.Title)
	}
	if sess.CWD != "/home/u/demo/api" {
		t.Errorf("cwd = %q", sess.CWD)
	}

	// 3 message blobs; the checkpoint blob is tolerated silently.
	if len(sink.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(sink.Messages))
	}
	if len(sink.Issues) != 0 {
		t.Errorf("issues = %v, want none", sink.Issues)
	}

	asst := sink.Messages[1]
	if asst.Role != canon.RoleAssistant || asst.Model != "claude-sonnet-5" {
		t.Errorf("assistant = %s/%s", asst.Role, asst.Model)
	}
	if asst.Usage == nil || asst.Usage.InputTokens != 1500 ||
		asst.Usage.CacheReadTokens != 6000 || asst.Usage.CacheWriteTokens != 250 {
		t.Errorf("usage = %+v", asst.Usage)
	}
	if asst.Text == "" {
		t.Error("assistant text not extracted from content blocks")
	}
	if sess.ModifiedAt.Before(sess.CreatedAt) {
		t.Errorf("times: %v / %v", sess.CreatedAt, sess.ModifiedAt)
	}
}
