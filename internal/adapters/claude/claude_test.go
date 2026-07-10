package claude

import (
	"context"
	"path/filepath"
	"sort"
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

	if len(sink.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sink.Sessions))
	}
	sess := sink.Sessions[0]
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
	if read.ResultStatus != "ok" || read.ResultExcerpt == "" {
		t.Errorf("read result = %q %q — pairing broken", read.ResultStatus, read.ResultExcerpt)
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

	// Branch changes fold to the last seen value.
	if sink.Sessions[0].GitBranch != "feat/limits" {
		t.Errorf("branch = %q, want feat/limits", sink.Sessions[0].GitBranch)
	}
}
