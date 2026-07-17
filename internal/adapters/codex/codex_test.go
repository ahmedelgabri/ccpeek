package codex

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

const sessionID = "01980000-aaaa-bbbb-cccc-codex0000001"

func parseFixture(t *testing.T) *agenttest.Sink {
	t.Helper()
	root, err := filepath.Abs("../../../testdata/agents/codex")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := New().Discover(context.Background(),
		agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("discovered %d sources, want 1", len(refs))
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), refs[0], sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sink
}

func TestParseSessionMeta(t *testing.T) {
	sink := parseFixture(t)
	if len(sink.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(sink.Sessions))
	}
	sess := sink.Sessions[0]
	if sess.ExternalID != sessionID {
		t.Errorf("external id = %q (must come from session_meta)", sess.ExternalID)
	}
	if sess.CWD != "/home/u/demo/api" || sess.GitBranch != "main" {
		t.Errorf("cwd/branch = %q/%q", sess.CWD, sess.GitBranch)
	}
	if sess.Title != "Profile the login endpoint and find the slow path" {
		t.Errorf("title = %q", sess.Title)
	}
}

func TestCumulativeTokenDeltas(t *testing.T) {
	sink := parseFixture(t)

	var usages []*canon.Usage
	for _, m := range sink.Messages {
		if m.Usage != nil {
			usages = append(usages, m.Usage)
		}
	}
	if len(usages) != 2 {
		t.Fatalf("usage-bearing entries = %d, want 2", len(usages))
	}

	// First token_count: last == total (first turn). Reasoning is a
	// SUBSET of output (450 output includes the 300 reasoning) — real
	// rollouts show total_tokens == input + output, so pricing output
	// alone is correct and adding reasoning would double-count.
	if usages[0].CacheReadTokens != 4000 || usages[0].InputTokens != 1200 ||
		usages[0].OutputTokens != 450 || usages[0].ReasoningTokens != 300 {
		t.Errorf("first turn usage = %+v", usages[0])
	}
	// Second: last_token_usage is authoritative (5800 input incl. 5200
	// cached → 600 uncached).
	if usages[1].InputTokens != 600 || usages[1].CacheReadTokens != 5200 ||
		usages[1].OutputTokens != 770 || usages[1].ReasoningTokens != 500 {
		t.Errorf("second turn usage = %+v", usages[1])
	}

	// Total across the session must equal the final cumulative counter:
	// 11000 input (9200 cached → 1800 uncached), 1220 output of which 800
	// reasoning.
	var in, cr, out, reas int64
	for _, u := range usages {
		in += u.InputTokens
		cr += u.CacheReadTokens
		out += u.OutputTokens
		reas += u.ReasoningTokens
	}
	if in != 1800 || cr != 9200 || out != 1220 || reas != 800 {
		t.Errorf("session totals = in %d, cacheRead %d, out %d, reasoning %d", in, cr, out, reas)
	}
	if out <= reas {
		t.Errorf("output %d must include reasoning %d (subset semantics)", out, reas)
	}

	// Model from turn_context applies to the assistant message.
	for _, m := range sink.Messages {
		if m.Role == canon.RoleAssistant && m.Kind == canon.KindMessage && m.Model != "gpt-5.2-codex" {
			t.Errorf("assistant model = %q", m.Model)
		}
	}
}

func TestShellCallPairing(t *testing.T) {
	sink := parseFixture(t)
	if len(sink.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(sink.ToolCalls))
	}
	tc := sink.ToolCalls[0]
	if tc.Name != "shell" || tc.Kind != canon.ToolShell {
		t.Errorf("call = %s/%s", tc.Name, tc.Kind)
	}
	if tc.ResultStatus != "ok" || tc.ResultExcerpt == "" {
		t.Errorf("result = %q %q", tc.ResultStatus, tc.ResultExcerpt)
	}

	// Bad line surfaced.
	if len(sink.Issues) != 1 || sink.Issues[0].Line != 9 {
		t.Errorf("issues = %+v, want one at line 9", sink.Issues)
	}
}

func TestCounterResetTreatedAsAbsolute(t *testing.T) {
	prev := &tokenUsage{InputTokens: 10000, TotalTokens: 12000}
	p := prev
	got := perTurnUsage(tokenCountInfo{Total: &tokenUsage{InputTokens: 500, TotalTokens: 600}}, &p)
	if got.InputTokens != 500 {
		t.Errorf("reset delta = %+v, want absolute values", got)
	}
}
