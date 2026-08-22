package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	adapter := New()
	refs, err := adapter.Discover(context.Background(),
		agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("discovered %d sources, want 1", len(refs))
	}
	if len(refs[0].CompanionPaths) != 1 || filepath.Base(refs[0].CompanionPaths[0]) != "session_index.jsonl" {
		t.Fatalf("rollout companions = %v, want session_index.jsonl", refs[0].CompanionPaths)
	}
	sink := &agenttest.Sink{}
	if err := adapter.Parse(context.Background(), refs[0], sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sink
}

func TestParseSessionMeta(t *testing.T) {
	sink := parseFixture(t)
	// Streaming parse: the session is emitted before its first child and
	// re-emitted at EOF; the LAST emit carries the folded metadata.
	if len(sink.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2 (initial + folded)", len(sink.Sessions))
	}
	sess := sink.Sessions[len(sink.Sessions)-1]
	if sess.ExternalID != sessionID {
		t.Errorf("external id = %q (must come from session_meta)", sess.ExternalID)
	}
	if sess.CWD != "/home/u/demo/api" || sess.GitBranch != "main" {
		t.Errorf("cwd/branch = %q/%q", sess.CWD, sess.GitBranch)
	}
	if sess.Title != "profile-login-performance" || !sess.TitleOverride {
		t.Errorf("title = %q override=%v, want Codex thread name", sess.Title, sess.TitleOverride)
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
	// Codex writes a shell command as an ARRAY of argv, wrapped in a shell
	// invocation. The commands browser reads canon.Command, so the wrapper
	// is unwrapped here — otherwise every Codex row rendered as the raw
	// JSON array `["bash","-lc","…"]`.
	if want := "go test -bench Login ./internal/auth"; tc.Command != want {
		t.Errorf("command = %q, want %q", tc.Command, want)
	}
	// Streaming parses never mutate an already-emitted call: the result
	// arrives as a ToolResult record the store pairs by call id.
	if len(sink.ToolResults) != 1 {
		t.Fatalf("tool results = %d, want 1", len(sink.ToolResults))
	}
	res := sink.ToolResults[0]
	if res.CallExternalID != tc.ExternalID || res.Status != "ok" || res.Excerpt == "" {
		t.Errorf("result = %+v (call id %q)", res, tc.ExternalID)
	}

	// Bad line surfaced.
	if len(sink.Issues) != 1 || sink.Issues[0].Line != 9 {
		t.Errorf("issues = %+v, want one at line 9", sink.Issues)
	}
}

func TestArgvRendersAsACommandLine(t *testing.T) {
	for _, tt := range []struct {
		name string
		argv string
		want string
	}{
		{"shell wrapper unwrapped", `{"command":["bash","-lc","ls -la | wc -l"]}`, "ls -la | wc -l"},
		{"sh -c too", `{"command":["sh","-c","echo hi"]}`, "echo hi"},
		{"plain argv joined", `{"command":["ls","-la"]}`, "ls -la"},
		{"single element", `{"command":["pwd"]}`, "pwd"},
		{"absent", `{}`, ""},
		{"not an array", `{"command":"already a string"}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolArgs(tt.argv).command(); got != tt.want {
				t.Errorf("command() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCounterResetTreatedAsAbsolute(t *testing.T) {
	st := tokenState{total: &tokenUsage{InputTokens: 10000, CacheWriteInputTokens: 2000, TotalTokens: 12000}}
	got := perTurnUsage(tokenCountInfo{Total: &tokenUsage{InputTokens: 500, CacheWriteInputTokens: 100, TotalTokens: 600}}, &st)
	if got.InputTokens != 500 || got.CacheWriteInputTokens != 100 {
		t.Errorf("reset delta = %+v, want absolute values", got)
	}
}

func TestCacheWritesAreSeparatedFromOrdinaryInput(t *testing.T) {
	const meta = `{"timestamp":"2026-07-06T09:00:00.000Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/home/u/demo/api"}}`
	first := `{"input_tokens":100,"cached_input_tokens":40,"cache_write_input_tokens":60,"output_tokens":10,"reasoning_output_tokens":5,"total_tokens":110}`
	second := `{"input_tokens":180,"cached_input_tokens":70,"cache_write_input_tokens":100,"output_tokens":30,"reasoning_output_tokens":12,"total_tokens":210}`

	src := writeRollout(
		t, meta,
		tokenCountLine("2026-07-06T09:00:10.000Z", first, "null"),
		tokenCountLine("2026-07-06T09:00:20.000Z", second, "null"),
	)
	usages := sessionUsage(t, src)
	if len(usages) != 2 {
		t.Fatalf("usage entries = %d, want 2", len(usages))
	}
	if got := *usages[0]; got.InputTokens != 0 || got.CacheReadTokens != 40 || got.CacheWriteTokens != 60 || got.OutputTokens != 10 || got.ReasoningTokens != 5 {
		t.Errorf("first usage = %+v", got)
	}
	// The cumulative delta is 80 input: 30 cache reads, 40 cache writes,
	// and 10 ordinary input.
	if got := *usages[1]; got.InputTokens != 10 || got.CacheReadTokens != 30 || got.CacheWriteTokens != 40 || got.OutputTokens != 20 || got.ReasoningTokens != 7 {
		t.Errorf("second usage = %+v", got)
	}
}

func TestCacheSubsetsCannotCreateNegativeInput(t *testing.T) {
	const meta = `{"timestamp":"2026-07-06T09:00:00.000Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/home/u/demo/api"}}`
	usage := `{"input_tokens":100,"cached_input_tokens":80,"cache_write_input_tokens":30,"output_tokens":10,"total_tokens":110}`
	src := writeRollout(t, meta, tokenCountLine("2026-07-06T09:00:10.000Z", usage, usage))
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatal(err)
	}
	var got *canon.Usage
	for _, message := range sink.Messages {
		if message.Usage != nil {
			got = message.Usage
			break
		}
	}
	if got == nil || got.InputTokens != 0 || got.CacheReadTokens != 80 || got.CacheWriteTokens != 30 {
		t.Fatalf("clamped usage = %+v", got)
	}
	if len(sink.Issues) != 1 || sink.Issues[0].Category != "usage" || !strings.Contains(sink.Issues[0].Detail, "clamping ordinary input") {
		t.Fatalf("issues = %+v, want visible subset warning", sink.Issues)
	}
}

func TestSessionTitleSkipsInjectedContext(t *testing.T) {
	src := writeRollout(
		t,
		`{"timestamp":"2026-07-06T09:00:00.000Z","type":"session_meta","payload":{"id":"`+sessionID+`","cwd":"/tmp/demo"}}`,
		`{"timestamp":"2026-07-06T09:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>synthetic context</recommended_plugins>"}]}}`,
		`{"timestamp":"2026-07-06T09:00:02.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"explain the failing build"}]}}`,
	)
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatal(err)
	}
	got := sink.Sessions[len(sink.Sessions)-1]
	if got.Title != "explain the failing build" || got.TitleOverride {
		t.Fatalf("session title = %q override=%v, want first real prompt", got.Title, got.TitleOverride)
	}
}

func TestCodexPromptTitleSkipsInjectedContext(t *testing.T) {
	for _, text := range []string{
		"<environment_context>metadata</environment_context>",
		"<permissions instructions=\"ask\">metadata</permissions>",
		"<recommended_plugins>metadata</recommended_plugins>",
		"<user_instructions>metadata</user_instructions>",
	} {
		if got := codexPromptTitle(text); got != "" {
			t.Errorf("codexPromptTitle(%q) = %q, want empty", text, got)
		}
	}
	if got := codexPromptTitle("  explain the failing build  "); got != "explain the failing build" {
		t.Errorf("real prompt title = %q", got)
	}
}

func TestParseVersionIncludesLatestNormalization(t *testing.T) {
	if got := New().ParseVersion(); got != 3 {
		t.Fatalf("ParseVersion() = %d, want 3", got)
	}
}

// writeRollout builds a rollout JSONL inside a throwaway CODEX_HOME and
// returns its SourceRef.
func writeRollout(t *testing.T, lines ...string) agent.SourceRef {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "07", "06")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-"+sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return agent.SourceRef{
		Root: agent.Root{Agent: Slug, Path: root},
		Path: path, Kind: agent.SourceFile,
	}
}

func tokenCountLine(ts string, total, last string) string {
	info := `{"total_token_usage":` + total + `,"last_token_usage":` + last + `}`
	return `{"timestamp":"` + ts + `","type":"event_msg","payload":` +
		`{"type":"token_count","info":` + info + `}}`
}

func sessionUsage(t *testing.T, src agent.SourceRef) []*canon.Usage {
	t.Helper()
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var out []*canon.Usage
	for _, m := range sink.Messages {
		if m.Usage != nil {
			out = append(out, m.Usage)
		}
	}
	return out
}

// A token_count event that repeats the previous one — same
// last_token_usage, cumulative total unmoved — is one turn emitted twice.
// Codex rows carry no content id and no request id, so the store's usage
// dedupe cannot catch it and the tokens land in the rollups twice. The
// cumulative total is what tells a repeat apart from a second turn that
// happened to cost the same.
func TestRepeatedLastTokenUsageCountsOnce(t *testing.T) {
	const (
		meta  = `{"timestamp":"2026-07-06T09:00:00.000Z","type":"session_meta","payload":{"id":"` + sessionID + `","cwd":"/home/u/demo/api"}}`
		turn1 = `{"input_tokens":1000,"cached_input_tokens":0,"output_tokens":200,"total_tokens":1200}`
		// Second turn: the SAME per-turn figures, but the cumulative total
		// has advanced by exactly that much — a real turn, not a repeat.
		cum2 = `{"input_tokens":2000,"cached_input_tokens":0,"output_tokens":400,"total_tokens":2400}`
	)

	t.Run("duplicate emission suppressed", func(t *testing.T) {
		src := writeRollout(
			t, meta,
			tokenCountLine("2026-07-06T09:00:10.000Z", turn1, turn1),
			tokenCountLine("2026-07-06T09:00:10.000Z", turn1, turn1),
		)
		usages := sessionUsage(t, src)
		if len(usages) != 1 {
			t.Fatalf("usage entries = %d, want 1 — the turn was counted twice", len(usages))
		}
		if usages[0].InputTokens != 1000 || usages[0].OutputTokens != 200 {
			t.Errorf("usage = %+v", usages[0])
		}
	})

	t.Run("identical turn with an advanced total still counts", func(t *testing.T) {
		src := writeRollout(
			t, meta,
			tokenCountLine("2026-07-06T09:00:10.000Z", turn1, turn1),
			tokenCountLine("2026-07-06T09:00:20.000Z", cum2, turn1),
		)
		usages := sessionUsage(t, src)
		if len(usages) != 2 {
			t.Fatalf("usage entries = %d, want 2 — a second real turn was swallowed", len(usages))
		}
		var out int64
		for _, u := range usages {
			out += u.OutputTokens
		}
		if out != 400 {
			t.Errorf("output tokens = %d, want 400 (the cumulative total)", out)
		}
	})

	t.Run("a delta-only count breaks the run of repeats", func(t *testing.T) {
		// No last_token_usage in the middle event: the identical Last that
		// follows is a new turn, not a repeat of the first.
		src := writeRollout(
			t, meta,
			tokenCountLine("2026-07-06T09:00:10.000Z", turn1, turn1),
			`{"timestamp":"2026-07-06T09:00:15.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":`+cum2+`}}}`,
			tokenCountLine("2026-07-06T09:00:20.000Z", cum2, turn1),
		)
		if usages := sessionUsage(t, src); len(usages) != 3 {
			t.Fatalf("usage entries = %d, want 3", len(usages))
		}
	})
}
