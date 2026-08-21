package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

	// Streaming parse: the session is emitted after the header and again
	// at EOF; the LAST emit carries the folded title and ModifiedAt.
	if len(sink.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (header + folded)", len(sink.Sessions))
	}
	sess := sink.Sessions[len(sink.Sessions)-1]
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
	if len(sink.Messages) != 14 {
		t.Fatalf("messages = %d, want 14", len(sink.Messages))
	}

	kinds := map[canon.MessageKind]int{}
	for _, m := range sink.Messages {
		kinds[m.Kind]++
	}
	if kinds[canon.KindMessage] != 10 || kinds[canon.KindModelChange] != 1 ||
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

func TestModernMessageAndSummaryUsage(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	lines := []string{
		`{"type":"session","id":"modern","version":3,"cwd":"/tmp/p","timestamp":"2026-08-21T10:00:00Z"}`,
		`{"type":"message","id":"m1","timestamp":"2026-08-21T10:00:01Z","message":{"role":"assistant","provider":"openai-codex","model":"gpt-5.6-sol","content":[{"type":"text","text":"ok"}],"usage":{"input":10,"output":2,"cacheRead":3,"cacheWrite":4,"cost":{"total":0.5}}}}`,
		`{"type":"compaction","id":"c1","parentId":"m1","timestamp":"2026-08-21T10:01:00Z","summary":"compact","usage":{"input":5,"output":1,"cacheRead":0,"cacheWrite":0,"cost":{"total":0.1}}}`,
		`{"type":"branch_summary","id":"b1","parentId":"c1","timestamp":"2026-08-21T10:02:00Z","fromId":"m1","summary":"branch","usage":{"input":7,"output":2,"cacheRead":1,"cacheWrite":0,"cost":{"total":0.2}}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), agent.SourceRef{
		Root: agent.Root{Agent: Slug, Path: root}, Path: path, Kind: agent.SourceFile,
	}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(sink.Messages))
	}
	for _, m := range sink.Messages {
		if m.Provider != "openai-codex" || m.Model != "gpt-5.6-sol" {
			t.Errorf("entry %s provider/model = %q/%q", m.ExternalID, m.Provider, m.Model)
		}
		if m.Usage == nil || m.Usage.ReportedCostUSD == nil {
			t.Errorf("entry %s missing usage/cost", m.ExternalID)
		}
	}
	if got := *sink.Messages[1].Usage.ReportedCostUSD; got != 0.1 {
		t.Errorf("compaction cost = %v", got)
	}
	if got := *sink.Messages[2].Usage.ReportedCostUSD; got != 0.2 {
		t.Errorf("branch summary cost = %v", got)
	}
}

func TestModelOnlyMessageClearsStaleProvider(t *testing.T) {
	provider, model := "openai-codex", "gpt-5.6-sol"
	entry := entry{
		Type: "message", ID: "m2",
		Message: json.RawMessage(`{"role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"switched"}]}`),
	}
	message, _, ok := New().convertEntry(entry, 0, &canon.Session{}, &provider, &model)
	if !ok {
		t.Fatal("message did not convert")
	}
	if message.Provider != "" || provider != "" || message.Model != "claude-sonnet-5" || model != "claude-sonnet-5" {
		t.Fatalf("message/state provider-model = %q/%q and %q/%q", message.Provider, message.Model, provider, model)
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

// TestParseToolCalls: Pi's toolCall blocks become canonical tool calls,
// paired with the role=toolResult messages that answer them.
func TestParseToolCalls(t *testing.T) {
	sink := parseFixture(t, "2026-07-01T10-00-00_"+mainSession+".jsonl")

	if len(sink.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(sink.ToolCalls))
	}
	bash, edit := sink.ToolCalls[0], sink.ToolCalls[1]

	if bash.Name != "bash" || bash.Kind != canon.ToolShell {
		t.Errorf("first call = %s/%s, want bash/shell", bash.Name, bash.Kind)
	}
	if bash.ExternalID != "call_pi_001" {
		t.Errorf("bash external id = %q", bash.ExternalID)
	}
	// Streaming parses never mutate an already-emitted call: results
	// arrive as ToolResult records the store pairs by call id.
	results := map[string]canon.ToolResult{}
	for _, r := range sink.ToolResults {
		results[r.CallExternalID] = r
	}
	if r := results["call_pi_001"]; r.Status != "ok" || !strings.Contains(r.Excerpt, "limiter.Take") {
		t.Errorf("bash result = %+v — toolResult pairing broken", r)
	}

	if edit.Name != "edit" || edit.Kind != canon.ToolFileEdit {
		t.Errorf("second call = %s/%s, want edit/file_edit", edit.Name, edit.Kind)
	}
	if edit.FilePath != "internal/auth/login.go" {
		t.Errorf("edit file path = %q (from arguments.path)", edit.FilePath)
	}
	if r := results[edit.ExternalID]; r.Status != "error" {
		t.Errorf("edit result = %+v, want error (isError:true)", r)
	}
}

// Pi spells a tool's outcome role "toolResult", which is not in canon's
// vocabulary (user/assistant/system/tool). Stored verbatim it left a value
// no role-keyed surface knows about — filters and rendering registers
// simply skipped Pi's tool results. The raw payload keeps Pi's spelling.
func TestToolResultRoleNormalized(t *testing.T) {
	sink := parseFixture(t, "2026-07-01T10-00-00_"+mainSession+".jsonl")

	vocabulary := map[canon.Role]bool{
		canon.RoleUser: true, canon.RoleAssistant: true,
		canon.RoleSystem: true, canon.RoleTool: true,
	}
	var toolRoled int
	for _, m := range sink.Messages {
		if !vocabulary[m.Role] {
			t.Errorf("entry %s carries out-of-vocabulary role %q", m.ExternalID, m.Role)
		}
		if m.Role != canon.RoleTool {
			continue
		}
		toolRoled++
		// The agent's own spelling survives where lossless rendering needs
		// it: in the raw payload, not in the canonical role.
		if !strings.Contains(string(m.Content), `"role":"toolResult"`) {
			t.Errorf("entry %s lost Pi's raw role in Content", m.ExternalID)
		}
	}
	// The fixture answers both tool calls.
	if toolRoled != 2 {
		t.Errorf("tool-roled messages = %d, want 2", toolRoled)
	}
}
