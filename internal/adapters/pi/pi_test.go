package pi

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

const (
	mainSession   = "9f8e7d6c-1111-2222-3333-444455556666"
	forkedSession = "1a2b3c4d-7777-8888-9999-000011112222"
)

func fixtureRoot(t *testing.T) agent.Root {
	t.Helper()
	path, err := filepath.Abs("../../../testdata/agents/pi")
	if err != nil {
		t.Fatal(err)
	}
	return agent.Root{Agent: Slug, Path: path, Origin: agent.RootFromDefault}
}

func parseFixture(t *testing.T, name string) *agenttest.Sink {
	t.Helper()
	refs, err := New().Discover(context.Background(), fixtureRoot(t))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	sink := &agenttest.Sink{}
	for _, ref := range refs {
		if filepath.Base(ref.Path) == name {
			if err := New().Parse(context.Background(), ref, sink); err != nil {
				t.Fatalf("Parse: %v", err)
			}
			return sink
		}
	}
	t.Fatalf("fixture %s not discovered", name)
	return nil
}

func TestDiscover(t *testing.T) {
	refs, err := New().Discover(context.Background(), fixtureRoot(t))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("found %d sources, want 2", len(refs))
	}
}

func TestParseMainSession(t *testing.T) {
	sink := parseFixture(t, "2026-07-01T10-00-00_"+mainSession+".jsonl")

	if len(sink.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sink.Sessions))
	}
	sess := sink.Sessions[0]
	if sess.ExternalID != mainSession {
		t.Errorf("external id = %q", sess.ExternalID)
	}
	if sess.CWD != "/home/u/demo/api" {
		t.Errorf("cwd = %q (must come from header, not directory name)", sess.CWD)
	}
	if sess.Title != "rate limiting work" {
		t.Errorf("title = %q, want session_info name", sess.Title)
	}
	if len(sink.Relations) != 0 {
		t.Errorf("relations = %v, want none for root session", sink.Relations)
	}

	// Entries: model_change, 6 messages, branch_summary, label, compaction
	// = 10 canonical messages (session_info folds into the title).
	if len(sink.Messages) != 10 {
		t.Fatalf("messages = %d, want 10", len(sink.Messages))
	}

	kinds := map[canon.MessageKind]int{}
	for _, m := range sink.Messages {
		kinds[m.Kind]++
	}
	if kinds[canon.KindMessage] != 6 || kinds[canon.KindModelChange] != 1 ||
		kinds[canon.KindCompaction] != 1 || kinds[canon.KindBranchPoint] != 1 ||
		kinds[canon.KindInfo] != 1 {
		t.Errorf("kind histogram = %v", kinds)
	}
}

func TestParseUsageWithReportedCost(t *testing.T) {
	sink := parseFixture(t, "2026-07-01T10-00-00_"+mainSession+".jsonl")

	var priced int
	for _, m := range sink.Messages {
		if m.Usage == nil {
			continue
		}
		priced++
		if m.Usage.ReportedCostUSD == nil {
			t.Errorf("assistant entry %s has usage but no reported cost", m.ExternalID)
		}
	}
	if priced != 3 {
		t.Fatalf("usage-bearing messages = %d, want 3", priced)
	}

	// Spot-check the first assistant entry.
	for _, m := range sink.Messages {
		if m.ExternalID != "aa000004" {
			continue
		}
		u := m.Usage
		if u.InputTokens != 1200 || u.OutputTokens != 180 ||
			u.CacheReadTokens != 8000 || u.CacheWriteTokens != 350 {
			t.Errorf("usage = %+v", u)
		}
		if *u.ReportedCostUSD != 0.0100125 {
			t.Errorf("reported cost = %v", *u.ReportedCostUSD)
		}
		if m.Model != "claude-sonnet-5" {
			t.Errorf("model = %q, want model_change state applied", m.Model)
		}
	}
}

func TestParseTreeBranching(t *testing.T) {
	sink := parseFixture(t, "2026-07-01T10-00-00_"+mainSession+".jsonl")

	parents := map[string]string{}
	for _, m := range sink.Messages {
		parents[m.ExternalID] = m.ParentExternalID
	}
	// In-place branch: aa000007 branches from aa000004, which already has
	// child aa000005.
	if parents["aa000005"] != "aa000004" || parents["aa000007"] != "aa000004" {
		t.Errorf("branch edges: aa000005→%q aa000007→%q, both want aa000004",
			parents["aa000005"], parents["aa000007"])
	}
}

func TestParseForkedSession(t *testing.T) {
	sink := parseFixture(t, "2026-07-02T09-00-00_"+forkedSession+".jsonl")

	if len(sink.Relations) != 1 {
		t.Fatalf("relations = %d, want 1", len(sink.Relations))
	}
	rel := sink.Relations[0]
	if rel.Kind != canon.RelForkOf || rel.FromExternalID != forkedSession ||
		rel.ToExternalID != mainSession {
		t.Errorf("relation = %+v", rel)
	}

	// Bad line surfaces as a diagnostic; unknown "custom" entry is
	// tolerated silently.
	if len(sink.Issues) != 1 {
		t.Fatalf("issues = %d, want 1: %v", len(sink.Issues), sink.Issues)
	}
	if sink.Issues[0].Line != 5 {
		t.Errorf("issue line = %d, want 5", sink.Issues[0].Line)
	}

	// Model comes from this file's own model_change.
	for _, m := range sink.Messages {
		if m.Kind == canon.KindMessage && m.Role == canon.RoleAssistant && m.Model != "claude-haiku-4-5" {
			t.Errorf("assistant model = %q", m.Model)
		}
	}
}
