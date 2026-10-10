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
	if sess.Title != "rate limiting work" || !sess.TitleOverride {
		t.Errorf("title = %q override=%v, want session_info name", sess.Title, sess.TitleOverride)
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

func TestSessionTitleSkipsSkillEnvelope(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl")
	lines := []string{
		`{"type":"session","id":"title-test","version":3,"cwd":"/tmp/demo","timestamp":"2026-08-22T10:00:00Z"}`,
		`{"type":"message","id":"m1","timestamp":"2026-08-22T10:00:01Z","message":{"role":"user","content":[{"type":"text","text":"<skill name=\"commit\">synthetic instructions</skill>"}]}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-08-22T10:00:02Z","message":{"role":"user","content":[{"type":"text","text":"explain the failing build"}]}}`,
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
	got := sink.Sessions[len(sink.Sessions)-1]
	if got.Title != "explain the failing build" || got.TitleOverride {
		t.Fatalf("session title = %q override=%v, want first real prompt", got.Title, got.TitleOverride)
	}
}

func TestPiPromptTitleSkipsSkillEnvelope(t *testing.T) {
	if got := piPromptTitle(`<skill name="commit">synthetic instructions</skill>`); got != "" {
		t.Errorf("piPromptTitle() = %q, want empty", got)
	}
	if got := piPromptTitle("  explain the failing build  "); got != "explain the failing build" {
		t.Errorf("real prompt title = %q", got)
	}
}

func TestParseVersionIncludesCodemode(t *testing.T) {
	if got := New().ParseVersion(); got != 8 {
		t.Fatalf("ParseVersion() = %d, want 8", got)
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

// A codemode call's script is its whole meaning, and the tools it ran
// exist only in the result's nestedCalls record — without both, a session
// that edits files through scripts shows no script and no file changes.
func TestParseCodemodeScriptAndNestedCalls(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "--home-u-demo--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"cm-1","timestamp":"2026-10-08T20:00:00.000Z","cwd":"/home/u/demo"}`,
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-08T20:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_cm","name":"codemode","arguments":{"code":"await tools.edit({path:\"a.go\",oldText:\"x\",newText:\"y\"});\nawait tools.bash({command:\"go test ./...\"});"}}]}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-08T20:00:02.000Z","message":{"role":"toolResult","toolCallId":"call_cm","toolName":"codemode","isError":false,"content":[{"type":"text","text":"Script completed"}],"nestedCalls":{"complete":true,"calls":[` +
			`{"id":"call_cm/1","name":"edit","status":"ok","arguments":{"path":"a.go","oldText":"x","newText":"y"},"durationMs":3},` +
			`{"id":"call_cm/2","name":"bash","status":"error","arguments":{"command":"go test ./..."},"durationMs":90,"error":"exit 1"}]}}}`,
	}
	path := filepath.Join(dir, "2026-10-08T20-00-00-000Z_cm-1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	src := agent.SourceRef{Path: path, Root: agent.Root{Agent: Slug, Path: root}}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if len(sink.ToolCalls) != 3 {
		t.Fatalf("tool calls = %d, want 3 (script + 2 nested)", len(sink.ToolCalls))
	}
	script, edit, bash := sink.ToolCalls[0], sink.ToolCalls[1], sink.ToolCalls[2]
	if script.Kind != canon.ToolScript || !strings.Contains(script.Command, "tools.edit") {
		t.Errorf("script = %s/%q, want script kind carrying the code", script.Kind, script.Command)
	}
	resultSeq := sink.Messages[1].Seq
	if edit.Kind != canon.ToolFileEdit || edit.FilePath != "a.go" || edit.NewText != "y" ||
		edit.ResultStatus != "ok" || edit.MessageSeq != resultSeq || edit.ExternalID != "call_cm/1" {
		t.Errorf("nested edit = %+v", edit)
	}
	if bash.Kind != canon.ToolShell || bash.Command != "go test ./..." ||
		bash.ResultStatus != "error" || bash.ResultExcerpt != "exit 1" {
		t.Errorf("nested bash = %+v", bash)
	}
}

// Pi marks a failed or aborted turn on the assistant message itself; the
// free-text error is classed like Claude's so both agents group together.
func TestStopReasonsBecomeEvents(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "--w--")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"session","version":3,"id":"ev-1","timestamp":"2026-10-08T20:00:00.000Z","cwd":"/w"}`,
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-08T20:00:01.000Z","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"Rate limit reached for gpt-6"}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-08T20:00:02.000Z","message":{"role":"assistant","content":[],"stopReason":"aborted","errorMessage":"Operation aborted"}}`,
		`{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-10-08T20:00:03.000Z","message":{"role":"assistant","content":[],"stopReason":"stop"}}`,
	}
	path := filepath.Join(dir, "2026-10-08T20-00-00-000Z_ev-1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), agent.SourceRef{Path: path, Root: agent.Root{Agent: Slug, Path: root}}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Events) != 2 {
		t.Fatalf("events = %+v, want error + abort", sink.Events)
	}
	if e := sink.Events[0]; e.Kind != canon.EventAPIError || e.Label != "rate_limit" || e.MessageSeq != 0 {
		t.Errorf("error event = %+v", e)
	}
	if e := sink.Events[1]; e.Kind != canon.EventAborted || e.MessageSeq != 1 {
		t.Errorf("abort event = %+v", e)
	}
}

// Pi's compaction entries record how full the context was; each becomes a
// compaction event anchored to its transcript entry.
func TestCompactionEntryBecomesEvent(t *testing.T) {
	sink := parseFixture(t, "2026-07-01T10-00-00_"+mainSession+".jsonl")
	var found []canon.SessionEvent
	for _, e := range sink.Events {
		if e.Kind == canon.EventCompaction {
			found = append(found, e)
		}
	}
	if len(found) != 1 || !strings.Contains(string(found[0].Payload), `"preTokens":9888`) {
		t.Errorf("compaction events = %+v, want one at 9888 tokens", found)
	}
}
