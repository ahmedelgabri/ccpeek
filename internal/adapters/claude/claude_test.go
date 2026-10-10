package claude

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func fixtureRoot(t *testing.T) agent.Root {
	t.Helper()
	path, err := filepath.Abs("../../../testdata/agents/claude-code")
	if err != nil {
		t.Fatal(err)
	}
	return agent.Root{Agent: Slug, Path: path, Origin: agent.RootFromDefault}
}

func discover(t *testing.T) []agent.SourceRef {
	t.Helper()
	refs, err := New().Discover(context.Background(), fixtureRoot(t))
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Path < refs[j].Path })
	return refs
}

func parseFixture(t *testing.T, sessionID string) *agenttest.Sink {
	t.Helper()
	sink := &agenttest.Sink{}
	src := agent.SourceRef{
		Root: fixtureRoot(t),
		Path: filepath.Join(fixtureRoot(t).Path, "projects", "-home-u-demo-api", sessionID+".jsonl"),
		Kind: agent.SourceFile,
	}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sink
}

func TestDiscoverFindsSessionFiles(t *testing.T) {
	refs := discover(t)
	sessions := 0
	for _, ref := range refs {
		if classify(ref.Root, ref.Path) == srcSession {
			sessions++
			if ref.Kind != agent.SourceFile {
				t.Errorf("%s kind = %q, want file", ref.Path, ref.Kind)
			}
		}
	}
	if sessions != 3 {
		t.Fatalf("Discover found %d session sources, want 3", sessions)
	}
}

func TestDiscoverMissingRootIsEmpty(t *testing.T) {
	refs, err := New().Discover(context.Background(),
		agent.Root{Agent: Slug, Path: t.TempDir(), Origin: agent.RootFromDefault})
	if err != nil || len(refs) != 0 {
		t.Fatalf("fresh install: refs=%v err=%v, want empty and nil", refs, err)
	}
}

func TestParseSessionAttributes(t *testing.T) {
	sink := parseFixture(t, "11111111-aaaa-bbbb-cccc-111111111111")

	// Streaming parse: the session is emitted before its first child and
	// re-emitted after EOF; the LAST emit carries the folded metadata.
	if len(sink.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (initial + folded)", len(sink.Sessions))
	}
	sess := sink.Sessions[len(sink.Sessions)-1]
	if sess.ExternalID != "11111111-aaaa-bbbb-cccc-111111111111" {
		t.Errorf("external id = %q", sess.ExternalID)
	}
	if sess.Title != "Add rate limiting to the login endpoint" {
		t.Errorf("title = %q", sess.Title)
	}
	if sess.CWD != "/home/u/demo/api" {
		t.Errorf("cwd = %q", sess.CWD)
	}
	if sess.GitBranch != "main" {
		t.Errorf("branch = %q", sess.GitBranch)
	}
	if sess.CreatedAt.IsZero() || sess.ModifiedAt.Before(sess.CreatedAt) {
		t.Errorf("timestamps: created=%v modified=%v", sess.CreatedAt, sess.ModifiedAt)
	}

	// 8 lines: 7 transcript entries (progress skipped), system line kept.
	if len(sink.Messages) != 7 {
		t.Fatalf("messages = %d, want 7", len(sink.Messages))
	}
	if len(sink.Issues) != 0 {
		t.Fatalf("issues = %v, want none", sink.Issues)
	}
}

func TestParseCapturesUsageAndModel(t *testing.T) {
	sink := parseFixture(t, "11111111-aaaa-bbbb-cccc-111111111111")

	var assistant []canon.Message
	for _, m := range sink.Messages {
		if m.Role == canon.RoleAssistant {
			assistant = append(assistant, m)
		}
	}
	if len(assistant) != 3 {
		t.Fatalf("assistant messages = %d, want 3", len(assistant))
	}

	first := assistant[0]
	if first.ContentID != "msg_alpha_1" {
		t.Errorf("content id = %q", first.ContentID)
	}
	if first.Model != "claude-sonnet-5" {
		t.Errorf("model = %q", first.Model)
	}
	if first.Usage == nil {
		t.Fatal("first assistant message has no usage")
	}
	if first.Usage.CacheWriteTokens != 2400 || first.Usage.InputTokens != 12 ||
		first.Usage.OutputTokens != 180 || first.Usage.RequestID != "req_alpha_1" {
		t.Errorf("usage = %+v", first.Usage)
	}

	second := assistant[1]
	if second.Usage == nil || second.Usage.CacheReadTokens != 2400 {
		t.Errorf("second usage = %+v, want cache read 2400", second.Usage)
	}

	// Model switches mid-session are per-message state.
	if assistant[2].Model != "claude-haiku-4-5" {
		t.Errorf("third model = %q", assistant[2].Model)
	}

	// Tree edges preserved.
	if first.ParentExternalID != "u-001" || first.ExternalID != "a-001" {
		t.Errorf("tree edge = %q→%q", first.ParentExternalID, first.ExternalID)
	}
}

func TestCacheWriteTTLAndLegacyCostOnly(t *testing.T) {
	cost := 0.25
	withUsage, _, _ := New().convertLine(rawLine{
		Type: "assistant", RequestID: "req", CostUSD: &cost,
		Message: json.RawMessage(`{"id":"msg","role":"assistant","model":"claude-sonnet-5","content":[],"usage":{"input_tokens":3,"output_tokens":4,"cache_creation_input_tokens":100,"cache_read_input_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":60}}}`),
	}, 0, "s")
	if withUsage.Usage == nil || withUsage.Usage.CacheWriteTokens != 100 ||
		withUsage.Usage.CacheWrite1hTokens != 60 {
		t.Fatalf("ttl usage = %+v", withUsage.Usage)
	}

	costOnly, _, _ := New().convertLine(rawLine{
		Type: "assistant", CostUSD: &cost,
		Message: json.RawMessage(`{"id":"legacy","role":"assistant","model":"claude-2","content":[]}`),
	}, 1, "s")
	if costOnly.Usage == nil || costOnly.Usage.ReportedCostUSD == nil ||
		*costOnly.Usage.ReportedCostUSD != cost {
		t.Fatalf("legacy cost-only usage = %+v", costOnly.Usage)
	}
}

func TestParseToolCallsWithResults(t *testing.T) {
	sink := parseFixture(t, "11111111-aaaa-bbbb-cccc-111111111111")

	if len(sink.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(sink.ToolCalls))
	}
	read, edit := sink.ToolCalls[0], sink.ToolCalls[1]

	if read.Name != "Read" || read.Kind != canon.ToolFileRead {
		t.Errorf("first call = %s/%s", read.Name, read.Kind)
	}
	if read.FilePath != "internal/auth/login.go" {
		t.Errorf("read file path = %q", read.FilePath)
	}
	// Streaming parses never mutate an already-emitted call: results
	// arrive as ToolResult records and the store pairs them by external
	// id. The read call's result must be among them.
	var readResult *canon.ToolResult
	for i := range sink.ToolResults {
		if sink.ToolResults[i].CallExternalID == read.ExternalID {
			readResult = &sink.ToolResults[i]
		}
	}
	if readResult == nil || readResult.Status != "ok" || readResult.Excerpt == "" {
		t.Errorf("read result record = %+v — pairing broken", readResult)
	}

	if edit.Name != "Edit" || edit.Kind != canon.ToolFileEdit {
		t.Errorf("second call = %s/%s", edit.Name, edit.Kind)
	}
	if edit.MessageSeq <= read.MessageSeq {
		t.Errorf("message seq ordering: read=%d edit=%d", read.MessageSeq, edit.MessageSeq)
	}
}

func TestParseSidechainAndDiagnostics(t *testing.T) {
	sink := parseFixture(t, "33333333-aaaa-bbbb-cccc-333333333333")

	var sidechain, main int
	for _, m := range sink.Messages {
		if m.IsSidechain {
			sidechain++
		} else {
			main++
		}
	}
	if sidechain != 2 {
		t.Errorf("sidechain messages = %d, want 2", sidechain)
	}
	if main != 4 {
		t.Errorf("main messages = %d, want 4", main)
	}

	// The corrupted line must surface as a warn diagnostic with its line
	// number, not abort the file.
	if len(sink.Issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(sink.Issues))
	}
	issue := sink.Issues[0]
	if issue.Severity != canon.SeverityWarn || issue.Category != "parse" || issue.Line != 6 {
		t.Errorf("issue = %+v", issue)
	}

	// Task tool normalizes to the subagent kind.
	var task *canon.ToolCall
	for i := range sink.ToolCalls {
		if sink.ToolCalls[i].Name == "Task" {
			task = &sink.ToolCalls[i]
		}
	}
	if task == nil || task.Kind != canon.ToolSubagent {
		t.Fatalf("Task call = %+v, want subagent kind", task)
	}

	// Unpriced models pass through verbatim for the pricing layer to flag.
	found := false
	for _, m := range sink.Messages {
		if m.Model == "experimental-audit-model" {
			found = true
		}
	}
	if !found {
		t.Error("sidechain model not captured")
	}
}

func TestParseResumedSessionKeepsDedupeKeys(t *testing.T) {
	sink := parseFixture(t, "22222222-aaaa-bbbb-cccc-222222222222")

	// The repeated assistant entry keeps the ORIGINAL content id and
	// request id so the store's usage dedupe can collapse it.
	var repeated *canon.Message
	for i := range sink.Messages {
		if sink.Messages[i].ContentID == "msg_alpha_3" {
			repeated = &sink.Messages[i]
		}
	}
	if repeated == nil {
		t.Fatal("repeated entry msg_alpha_3 not found")
	}
	if repeated.Usage == nil || repeated.Usage.RequestID != "req_alpha_3" {
		t.Errorf("repeated usage = %+v", repeated.Usage)
	}
	if repeated.ExternalID == "a-003" {
		t.Error("entry uuid should differ from the original session's entry")
	}

	// Branch changes fold to the last seen value (on the final emit).
	if got := sink.Sessions[len(sink.Sessions)-1].GitBranch; got != "feat/limits" {
		t.Errorf("branch = %q, want feat/limits", got)
	}
}

// tailFixture builds a session file in a temp root for cursor tests. The
// initial content ends with an unanswered tool_use so a later-appended
// tool_result must pair across the parse boundary.
func tailFixture(t *testing.T) (agent.SourceRef, string) {
	t.Helper()
	dir := t.TempDir()
	proj := filepath.Join(dir, "projects", "-home-u-app")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(proj, "99999999-aaaa-bbbb-cccc-999999999999.jsonl")
	initial := `{"type":"user","uuid":"u-1","sessionId":"99999999-aaaa-bbbb-cccc-999999999999","cwd":"/home/u/app","gitBranch":"main","timestamp":"2026-07-01T10:00:00.000Z","message":{"role":"user","content":"start the job"}}
{"type":"assistant","uuid":"a-1","parentUuid":"u-1","timestamp":"2026-07-01T10:00:05.000Z","message":{"id":"msg_1","role":"assistant","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"toolu_01","name":"Bash","input":{"command":"ls"}}]}}
`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	src := agent.SourceRef{
		Root: agent.Root{Agent: Slug, Path: dir, Origin: agent.RootFromDefault},
		Path: path,
		Kind: agent.SourceFile,
	}
	return src, path
}

func appendTo(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestSessionTitleSkipsSyntheticLocalCommands(t *testing.T) {
	src, path := tailFixture(t)
	body := `{"type":"user","uuid":"u-1","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"<bash-input>nix-collect-garbage -d</bash-input>"}}
{"type":"user","uuid":"u-2","timestamp":"2026-07-01T10:00:01Z","message":{"role":"user","content":"<command-name>/clear</command-name>"}}
{"type":"user","uuid":"u-3","timestamp":"2026-07-01T10:00:02Z","message":{"role":"user","content":"explain the failing build"}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatal(err)
	}
	got := sink.Sessions[len(sink.Sessions)-1].Title
	if got != "explain the failing build" {
		t.Fatalf("session title = %q, want first real prompt", got)
	}
}

func TestSessionTitlePrefersNativeMetadata(t *testing.T) {
	src, path := tailFixture(t)
	appendTo(t, path, `{"type":"ai-title","aiTitle":"AI summary","sessionId":"99999999-aaaa-bbbb-cccc-999999999999"}
{"type":"custom-title","customTitle":"user-assigned-name","sessionId":"99999999-aaaa-bbbb-cccc-999999999999"}
`)

	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Sessions) == 0 {
		t.Fatal("no session emitted")
	}
	got := sink.Sessions[len(sink.Sessions)-1]
	if got.Title != "user-assigned-name" || !got.TitleOverride {
		t.Fatalf("session title = %q override=%v, want custom title override", got.Title, got.TitleOverride)
	}
}

func TestParseTailEmitsCustomTitleWithoutMessages(t *testing.T) {
	src, path := tailFixture(t)
	state, err := New().ParseTail(context.Background(), src, agent.TailState{}, &agenttest.Sink{})
	if err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, `{"type":"custom-title","customTitle":"renamed-later","sessionId":"99999999-aaaa-bbbb-cccc-999999999999"}
`)

	sink := &agenttest.Sink{}
	if _, err := New().ParseTail(context.Background(), src, state, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Messages) != 0 || len(sink.Sessions) != 1 {
		t.Fatalf("tail emitted %d messages and %d sessions, want metadata-only session update", len(sink.Messages), len(sink.Sessions))
	}
	if got := sink.Sessions[0]; got.Title != "renamed-later" || !got.TitleOverride {
		t.Fatalf("tail title = %q override=%v, want explicit rename", got.Title, got.TitleOverride)
	}
}

func TestClaudePromptTitleSkipsSyntheticLocalCommands(t *testing.T) {
	for _, text := range []string{
		"<local-command-caveat>Caveat: generated locally</local-command-caveat>",
		"<command-name>/clear</command-name>",
		"<local-command-stdout></local-command-stdout>",
		"<bash-input>nix-collect-garbage -d</bash-input>",
		"<bash-stdout>store paths deleted</bash-stdout>",
		"<bash-stderr>warning</bash-stderr>",
		"<system-reminder>metadata</system-reminder>",
	} {
		if got := claudePromptTitle(text); got != "" {
			t.Errorf("claudePromptTitle(%q) = %q, want empty", text, got)
		}
	}
	if got := claudePromptTitle("  explain the failing build  "); got != "explain the failing build" {
		t.Errorf("real prompt title = %q", got)
	}
}

func TestParseTailEmitsOnlyAppendedRecords(t *testing.T) {
	src, path := tailFixture(t)
	ctx := context.Background()

	first := &agenttest.Sink{}
	state, err := New().ParseTail(ctx, src, agent.TailState{}, first)
	if err != nil {
		t.Fatalf("full ParseTail: %v", err)
	}
	fi, _ := os.Stat(path)
	if state.Offset != fi.Size() || state.MessageSeq != 2 || state.ToolSeq != 1 {
		t.Fatalf("initial cursor = %+v, want offset %d, 2 messages, 1 call", state, fi.Size())
	}
	if len(first.Messages) != 2 || len(first.ToolCalls) != 1 || len(first.ToolResults) != 0 {
		t.Fatalf("full parse emitted %d/%d/%d msgs/calls/results",
			len(first.Messages), len(first.ToolCalls), len(first.ToolResults))
	}
	if first.ToolCalls[0].ExternalID != "toolu_01" {
		t.Errorf("call external id = %q", first.ToolCalls[0].ExternalID)
	}

	// The appended entries: the result for the pre-boundary call, then a
	// fresh call answered within the tail.
	appendTo(t, path, `{"type":"user","uuid":"u-2","parentUuid":"a-1","timestamp":"2026-07-01T10:01:00.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"file.txt"}]}}
{"type":"assistant","uuid":"a-2","parentUuid":"u-2","timestamp":"2026-07-01T10:01:05.000Z","message":{"id":"msg_2","role":"assistant","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"toolu_02","name":"Read","input":{"file_path":"x.go"}}]}}
{"type":"user","uuid":"u-3","parentUuid":"a-2","timestamp":"2026-07-01T10:01:10.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_02","content":"boom","is_error":true}]}}
`)

	second := &agenttest.Sink{}
	newState, err := New().ParseTail(ctx, src, state, second)
	if err != nil {
		t.Fatalf("tail ParseTail: %v", err)
	}
	fi, _ = os.Stat(path)
	if newState.Offset != fi.Size() {
		t.Errorf("cursor offset = %d, want %d", newState.Offset, fi.Size())
	}

	// Only the three appended messages, with continued sequence numbers.
	if len(second.Messages) != 3 {
		t.Fatalf("tail emitted %d messages, want 3", len(second.Messages))
	}
	if second.Messages[0].Seq != 2 || second.Messages[2].Seq != 4 {
		t.Errorf("tail message seqs = %d..%d, want 2..4",
			second.Messages[0].Seq, second.Messages[2].Seq)
	}

	// The new call continues the tool seq; its result arrives as a
	// ToolResult record like every other (streaming parses never mutate
	// already-emitted calls — the store pairs by external id).
	if len(second.ToolCalls) != 1 {
		t.Fatalf("tail emitted %d tool calls, want 1", len(second.ToolCalls))
	}
	call := second.ToolCalls[0]
	if call.Seq != 1 || call.ExternalID != "toolu_02" {
		t.Errorf("tail call = %+v", call)
	}

	// Both results cross as ToolResult records: the pre-boundary call's
	// and the new call's own.
	if len(second.ToolResults) != 2 {
		t.Fatalf("tail emitted %d tool results, want 2", len(second.ToolResults))
	}
	byCall := map[string]canon.ToolResult{}
	for _, r := range second.ToolResults {
		byCall[r.CallExternalID] = r
	}
	if res := byCall["toolu_01"]; res.Status != "ok" || res.Excerpt != "file.txt" {
		t.Errorf("late result = %+v", res)
	}
	if res := byCall["toolu_02"]; res.Status != "error" {
		t.Errorf("in-pass result = %+v", res)
	}

	// Session attributes folded from the tail: modification advances on
	// the final emit.
	if len(second.Sessions) == 0 || second.Sessions[len(second.Sessions)-1].ModifiedAt.IsZero() {
		t.Fatalf("tail sessions = %+v", second.Sessions)
	}
}

func TestParseTailRejectsRewrittenPrefix(t *testing.T) {
	src, path := tailFixture(t)
	ctx := context.Background()

	state, err := New().ParseTail(ctx, src, agent.TailState{}, &agenttest.Sink{})
	if err != nil {
		t.Fatal(err)
	}

	// Rewrite a byte inside the already-parsed prefix.
	data, _ := os.ReadFile(path)
	data[10] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New().ParseTail(ctx, src, state, &agenttest.Sink{}); !errors.Is(err, agent.ErrTailInvalid) {
		t.Fatalf("rewritten prefix error = %v, want ErrTailInvalid", err)
	}

	// A truncated file (shorter than the cursor) is also unresumable.
	if err := os.WriteFile(path, data[:20], 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New().ParseTail(ctx, src, state, &agenttest.Sink{}); !errors.Is(err, agent.ErrTailInvalid) {
		t.Fatalf("truncated file error = %v, want ErrTailInvalid", err)
	}
}

func TestParseTailLeavesPartialLineForNextPass(t *testing.T) {
	src, path := tailFixture(t)
	ctx := context.Background()

	state, err := New().ParseTail(ctx, src, agent.TailState{}, &agenttest.Sink{})
	if err != nil {
		t.Fatal(err)
	}

	// A mid-write append: no trailing newline yet.
	appendTo(t, path, `{"type":"user","uuid":"u-2","message":{"role":"user","content":"half-writ`)
	sink := &agenttest.Sink{}
	mid, err := New().ParseTail(ctx, src, state, sink)
	if err != nil {
		t.Fatal(err)
	}
	if mid.Offset != state.Offset || len(sink.Messages) != 0 {
		t.Fatalf("partial line consumed: offset %d→%d, %d messages",
			state.Offset, mid.Offset, len(sink.Messages))
	}

	// The write completes; the next pass picks up the whole line.
	appendTo(t, path, `ten"},"timestamp":"2026-07-01T10:02:00.000Z"}`+"\n")
	sink = &agenttest.Sink{}
	done, err := New().ParseTail(ctx, src, mid, sink)
	if err != nil {
		t.Fatal(err)
	}
	if len(sink.Messages) != 1 || sink.Messages[0].Seq != 2 {
		t.Fatalf("completed line parse = %d messages (seq %v), want 1 at seq 2",
			len(sink.Messages), sink.Messages)
	}
	fi, _ := os.Stat(path)
	if done.Offset != fi.Size() {
		t.Errorf("final offset = %d, want %d", done.Offset, fi.Size())
	}
}

// Identity is the file name — stable, and known before a line parses.
// The sessionId inside the JSONL is therefore never a fallback (the guard
// that pretended otherwise could not fire), but a disagreement means the
// transcript was copied or renamed and is worth saying out loud once.
func TestSessionIDMismatchWarnsOnceAndKeepsFilenameIdentity(t *testing.T) {
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-home-u-x")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	const fileID = "aaaaaaaa-1111-2222-3333-444444444444"
	const innerID = "bbbbbbbb-9999-8888-7777-666666666666"
	path := filepath.Join(projects, fileID+".jsonl")

	var body strings.Builder
	for i := range 3 {
		body.WriteString(`{"type":"user","uuid":"u` + strconv.Itoa(i) +
			`","sessionId":"` + innerID +
			`","timestamp":"2026-07-01T10:00:0` + strconv.Itoa(i) +
			`Z","cwd":"/home/u/x","message":{"role":"user","content":"hi"}}` + "\n")
	}
	if err := os.WriteFile(path, []byte(body.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	sink := &agenttest.Sink{}
	src := agent.SourceRef{
		Root: agent.Root{Agent: Slug, Path: dir},
		Path: path,
		Kind: agent.SourceFile,
	}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Identity stays the file name.
	if len(sink.Sessions) == 0 {
		t.Fatal("no session emitted")
	}
	for _, s := range sink.Sessions {
		if s.ExternalID != fileID {
			t.Errorf("session id = %q, want the file name %q", s.ExternalID, fileID)
		}
	}

	// Exactly one warning, however many lines disagree.
	var warnings []canon.Issue
	for _, is := range sink.Issues {
		if is.Category == "identity" {
			warnings = append(warnings, is)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("identity warnings = %d, want 1: %+v", len(warnings), sink.Issues)
	}
	if warnings[0].Severity != canon.SeverityWarn {
		t.Errorf("severity = %q, want warn", warnings[0].Severity)
	}
	for _, want := range []string{innerID, fileID} {
		if !strings.Contains(warnings[0].Detail, want) {
			t.Errorf("warning %q does not mention %q", warnings[0].Detail, want)
		}
	}
}

// The normal case — file name and sessionId agree — stays silent.
func TestMatchingSessionIDIsSilent(t *testing.T) {
	for _, ref := range discover(t) {
		sink := &agenttest.Sink{}
		if err := New().Parse(context.Background(), ref, sink); err != nil {
			t.Fatalf("Parse(%s): %v", ref.Path, err)
		}
		for _, is := range sink.Issues {
			if is.Category == "identity" {
				t.Errorf("unexpected identity warning on %s: %+v", ref.Path, is)
			}
		}
	}
}

// Subagent transcripts live beside their parent session and were never
// discovered, so their turns — and their cost — were missing. Each must
// become its own session linked back to whoever spawned it: the main
// session, or the parent subagent named by meta.json. Their lines carry the
// parent's sessionId by design, which must not read as a renamed file.
func TestSubagentTranscriptsBecomeLinkedSessions(t *testing.T) {
	const parent = "11111111-2222-3333-4444-555555555555"
	root := t.TempDir()
	sessDir := filepath.Join(root, "projects", "-w", parent)
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(sessDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	line := func(typ, uuid string) string {
		return `{"type":"` + typ + `","isSidechain":true,"sessionId":"` + parent + `","uuid":"` + uuid +
			`","timestamp":"2026-10-01T10:00:00Z","message":{"role":"` + typ + `","content":"do the thing"}}` + "\n"
	}
	write("../"+parent+".jsonl", strings.Replace(line("user", "p1"), `"isSidechain":true`, `"isSidechain":false`, 1))
	write("subagents/agent-a1.jsonl", line("user", "s1")+line("assistant", "s2"))
	write("subagents/agent-a1.meta.json", `{"agentType":"Explore","description":"Map the indexer","toolUseId":"toolu_1"}`)
	write("subagents/workflows/wf_1/agent-b2.jsonl", line("user", "w1"))
	write("subagents/workflows/wf_1/agent-b2.meta.json", `{"agentType":"workflow-subagent","parentAgentId":"a1"}`)
	write("subagents/workflows/wf_1/journal.jsonl", "{}\n")

	r := agent.Root{Agent: Slug, Path: root}
	refs, err := New().Discover(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	for _, ref := range refs {
		if err := New().Parse(context.Background(), ref, sink); err != nil {
			t.Fatalf("Parse %s: %v", ref.Path, err)
		}
	}

	titles := map[string]string{}
	for _, s := range sink.Sessions {
		titles[s.ExternalID] = s.Title
	}
	if len(titles) != 3 || titles["agent-a1"] != "Map the indexer" || titles["agent-b2"] != "do the thing" {
		t.Errorf("sessions = %v, want the parent plus agent-a1 (titled by its description) and agent-b2", titles)
	}
	edges := map[string]string{}
	for _, rel := range sink.Relations {
		if rel.Kind == canon.RelSubagentOf {
			edges[rel.FromExternalID] = rel.ToExternalID
		}
	}
	if edges["agent-a1"] != parent || edges["agent-b2"] != "agent-a1" {
		t.Errorf("subagent_of edges = %v", edges)
	}
	for _, m := range sink.Messages {
		if m.IsSidechain {
			t.Errorf("message %s still flagged sidechain inside its own session", m.ExternalID)
		}
	}
	if len(sink.Issues) != 0 {
		t.Errorf("issues = %+v, want none (parent sessionId is expected)", sink.Issues)
	}
}

// The oldest layout kept subagents beside their parent as
// projects/<p>/agent-<id>.jsonl with no meta.json; their lines carry the
// parent's sessionId, which is the only link there is.
func TestLegacyTopLevelSubagentLinksByLineSessionID(t *testing.T) {
	const parent = "66666666-2222-3333-4444-555555555555"
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","isSidechain":true,"sessionId":"` + parent + `","uuid":"s1","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"look around"}}` + "\n"
	path := filepath.Join(dir, "agent-abc1234.jsonl")
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	src := agent.SourceRef{Root: agent.Root{Agent: Slug, Path: root}, Path: path, Kind: agent.SourceFile}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Relations) != 1 || sink.Relations[0].Kind != canon.RelSubagentOf ||
		sink.Relations[0].FromExternalID != "agent-abc1234" || sink.Relations[0].ToExternalID != parent {
		t.Errorf("relations = %+v, want agent-abc1234 subagent_of the line sessionId", sink.Relations)
	}
	if len(sink.Issues) != 0 {
		t.Errorf("issues = %+v, want none", sink.Issues)
	}
	if got := sink.Sessions[len(sink.Sessions)-1].Title; got != "look around" {
		t.Errorf("title = %q, want the first prompt", got)
	}
}

// Resuming a session under a new id copies its history in, every line
// still carrying the id it was written under; a chain of resumes copies
// all ancestors, oldest first. The direct parent is the LAST foreign id,
// and its last copied entry is where this session branched off. That is
// lineage, not a renamed file, so it must not raise the identity warning.
func TestCopiedHistoryLinksTheDirectParent(t *testing.T) {
	const (
		root1 = "11111111-0000-0000-0000-000000000001"
		root2 = "22222222-0000-0000-0000-000000000002"
		self  = "33333333-0000-0000-0000-000000000003"
	)
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-w")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(session, uuid string) string {
		return `{"type":"user","uuid":"` + uuid + `","sessionId":"` + session +
			`","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"hi"}}` + "\n"
	}
	path := filepath.Join(projects, self+".jsonl")
	if err := os.WriteFile(path, []byte(line(root1, "a")+line(root2, "b")+line(root2, "c")+line(self, "d")), 0o600); err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	src := agent.SourceRef{Root: agent.Root{Agent: Slug, Path: dir}, Path: path, Kind: agent.SourceFile}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Relations) != 1 {
		t.Fatalf("relations = %+v, want one resumed_from edge", sink.Relations)
	}
	rel := sink.Relations[0]
	var ev struct {
		BranchUUID string `json:"branchUuid"`
	}
	_ = json.Unmarshal(rel.Evidence, &ev)
	if rel.Kind != canon.RelResumedFrom || rel.FromExternalID != self || rel.ToExternalID != root2 || ev.BranchUUID != "c" {
		t.Errorf("relation = %+v (branch %q), want %s resumed_from %s at c", rel, ev.BranchUUID, self, root2)
	}
	if len(sink.Issues) != 0 {
		t.Errorf("issues = %+v, want none for copied history", sink.Issues)
	}
}

// A legacy subagent can outlive a resume of its parent, so its lines name
// two parent ids. Neither is copied history: no resume edge, no warning.
func TestSubagentSpanningAParentResumeIsNotACopy(t *testing.T) {
	const parent, resumed = "12121212-0000-0000-0000-000000000001", "12121212-0000-0000-0000-000000000002"
	root := t.TempDir()
	dir := filepath.Join(root, "projects", "-w")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := func(session, uuid string) string {
		return `{"type":"user","isSidechain":true,"sessionId":"` + session + `","uuid":"` + uuid +
			`","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"go"}}` + "\n"
	}
	path := filepath.Join(dir, "agent-feed123.jsonl")
	if err := os.WriteFile(path, []byte(line(parent, "a")+line(resumed, "b")), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), agent.SourceRef{Root: agent.Root{Agent: Slug, Path: root}, Path: path, Kind: agent.SourceFile}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Relations) != 1 || sink.Relations[0].Kind != canon.RelSubagentOf || sink.Relations[0].ToExternalID != parent {
		t.Errorf("relations = %+v, want only subagent_of the first parent", sink.Relations)
	}
	if len(sink.Issues) != 0 {
		t.Errorf("issues = %+v, want none", sink.Issues)
	}
}

// Failed requests, interrupted turns and refused tool calls are written as
// ordinary-looking transcript lines; each must also surface as an event
// anchored to its line, so errors can be counted across sessions.
func TestErrorAbortAndDenialLinesBecomeEvents(t *testing.T) {
	const id = "77777777-0000-0000-0000-000000000001"
	dir := t.TempDir()
	projects := filepath.Join(dir, "projects", "-w")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	head := `"sessionId":"` + id + `","timestamp":"2026-10-01T10:00:00Z",`
	body := `{"type":"assistant",` + head + `"uuid":"e1","isApiErrorMessage":true,"apiErrorStatus":429,"error":"rate_limit","quotaLimits":{"rateLimitType":"five_hour"},"message":{"id":"x","role":"assistant","model":"<synthetic>","content":[{"type":"text","text":"You've hit your session limit"}]}}` + "\n" +
		`{"type":"user",` + head + `"uuid":"e2","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}` + "\n" +
		`{"type":"user",` + head + `"uuid":"e3","toolDenialKind":"user-rejected","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"The user doesn't want to proceed"}]}}` + "\n"
	path := filepath.Join(projects, id+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), agent.SourceRef{Root: agent.Root{Agent: Slug, Path: dir}, Path: path, Kind: agent.SourceFile}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Events) != 3 {
		t.Fatalf("events = %+v, want 3", sink.Events)
	}
	apiErr, abort, denial := sink.Events[0], sink.Events[1], sink.Events[2]
	if apiErr.Kind != canon.EventAPIError || apiErr.Label != "rate_limit" || apiErr.Status != "429" ||
		apiErr.MessageSeq != 0 || !strings.Contains(string(apiErr.Payload), "five_hour") {
		t.Errorf("api error = %+v", apiErr)
	}
	if abort.Kind != canon.EventAborted || abort.MessageSeq != 1 {
		t.Errorf("abort = %+v", abort)
	}
	if denial.Kind != canon.EventToolDenied || denial.Label != "user-rejected" || !strings.Contains(denial.Detail, "proceed") {
		t.Errorf("denial = %+v", denial)
	}
}
