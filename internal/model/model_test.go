package model

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMessagePayloadIsString(t *testing.T) {
	// String content
	msg := MessagePayload{Content: json.RawMessage(`"hello"`)}
	if !msg.IsString() {
		t.Error("expected IsString=true for string content")
	}
	if msg.ContentText() != "hello" {
		t.Errorf("expected ContentText=hello, got %q", msg.ContentText())
	}

	// Array content
	msg2 := MessagePayload{Content: json.RawMessage(`[{"type":"text","text":"hi"}]`)}
	if msg2.IsString() {
		t.Error("expected IsString=false for array content")
	}
	if msg2.ContentText() != "hi" {
		t.Errorf("expected ContentText=hi, got %q", msg2.ContentText())
	}

	blocks := msg2.ContentBlocks()
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "hi" {
		t.Errorf("unexpected block: %+v", blocks[0])
	}
}

func TestToolResultText(t *testing.T) {
	// String content
	b := ContentBlock{Content: json.RawMessage(`"some output"`)}
	if b.ToolResultText() != "some output" {
		t.Errorf("expected 'some output', got %q", b.ToolResultText())
	}

	// Array content
	b2 := ContentBlock{Content: json.RawMessage(`[{"type":"text","text":"line1"},{"type":"text","text":"line2"}]`)}
	if b2.ToolResultText() != "line1\nline2" {
		t.Errorf("expected 'line1\\nline2', got %q", b2.ToolResultText())
	}
}

func TestParseRealConversation(t *testing.T) {
	path := os.Getenv("TEST_CONVERSATION_FILE")
	if path == "" {
		t.Skip("set TEST_CONVERSATION_FILE to test with real data")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var msgs []ConversationMessage
	if err := json.Unmarshal(data, &msgs); err != nil {
		t.Fatal(err)
	}

	t.Logf("Parsed %d messages", len(msgs))
	for i, m := range msgs[:min(5, len(msgs))] {
		t.Logf("msg[%d] type=%s isString=%v blocks=%d text=%q",
			i, m.Type, m.Message.IsString(), len(m.Message.ContentBlocks()), truncStr(m.Message.ContentText(), 80))
	}
}

func TestFormatCommands(t *testing.T) {
	cmds := []CommandEntry{
		{Command: "ls -la", Timestamp: "2025-01-15T10:30:00Z"},
		{Command: "echo hello", Timestamp: "2025-01-15T10:31:00Z"},
	}

	t.Run("plain", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "plain")
		out := buf.String()
		if !strings.Contains(out, "ls -la\n") {
			t.Error("plain format missing command")
		}
		if strings.Contains(out, ":0;") {
			t.Error("plain format should not have zsh timestamps")
		}
	})

	t.Run("bash", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "bash")
		if !strings.Contains(buf.String(), "ls -la\n") {
			t.Error("bash format missing command")
		}
	})

	t.Run("zsh", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "zsh")
		out := buf.String()
		if !strings.Contains(out, ":0;ls -la") {
			t.Error("zsh format missing command with timestamp")
		}
		if !strings.HasPrefix(out, ": ") {
			t.Error("zsh format should start with ': '")
		}
	})

	t.Run("zsh_multiline", func(t *testing.T) {
		multiCmds := []CommandEntry{
			{Command: "git log --shortstat |\nawk '/^ [0-9]/ { f += $1 }'\nEND", Timestamp: "2025-01-15T10:30:00Z"},
		}
		var buf bytes.Buffer
		_ = FormatCommands(&buf, multiCmds, "zsh")
		out := buf.String()
		if !strings.Contains(out, "\\\n") {
			t.Error("zsh format should escape newlines in multi-line commands with backslash")
		}
		// Each intermediate newline gets a backslash, but the final newline (end of entry) does not
		lines := strings.SplitAfter(out, "\n")
		// Remove the trailing empty element from the split
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		for i, line := range lines {
			if i < len(lines)-1 {
				if !strings.HasSuffix(line, "\\\n") {
					t.Errorf("intermediate line %d should end with backslash-newline, got %q", i, line)
				}
			} else {
				if strings.HasSuffix(line, "\\\n") {
					t.Error("last line should not end with backslash-newline")
				}
			}
		}
	})

	t.Run("fish", func(t *testing.T) {
		var buf bytes.Buffer
		_ = FormatCommands(&buf, cmds, "fish")
		out := buf.String()
		if !strings.Contains(out, "- cmd: ls -la") {
			t.Error("fish format missing command")
		}
		if !strings.Contains(out, "when: ") {
			t.Error("fish format missing timestamp")
		}
	})
}

func TestSplitSourceID(t *testing.T) {
	tests := []struct {
		input    string
		wantSess string
		wantTS   string
	}{
		{"sess-123@2024-01-15T10:00:00Z", "sess-123", "2024-01-15T10:00:00Z"},
		{"sess-123", "sess-123", ""},
		{"", "", ""},
		{"a@b@c", "a@b", "c"},
	}

	for _, tt := range tests {
		sess, ts := splitSourceID(tt.input)
		if sess != tt.wantSess || ts != tt.wantTS {
			t.Errorf("splitSourceID(%q) = (%q, %q), want (%q, %q)",
				tt.input, sess, ts, tt.wantSess, tt.wantTS)
		}
	}
}

func TestAnchorize(t *testing.T) {
	tests := []struct {
		prefix string
		value  string
		want   string
	}{
		{"msg", "2024-01-15T10:30:00.123Z", "msg-2024-01-15T10-30-00-123Z"},
		{"cmd", "2024-01-15T10:30:00Z", "cmd-2024-01-15T10-30-00Z"},
		{"s", "aaaaaaaa-bbbb", "s-aaaaaaaa-bbbb"},
	}

	for _, tt := range tests {
		got := anchorize(tt.prefix, tt.value)
		if got != tt.want {
			t.Errorf("anchorize(%q, %q) = %q, want %q", tt.prefix, tt.value, got, tt.want)
		}
	}
}

func TestSourceURL(t *testing.T) {
	tests := []struct {
		name    string
		finding ScanFinding
		want    string
	}{
		{
			"message with timestamp",
			ScanFinding{SourceType: "message", SourceID: "sess-123@2024-01-15T10:00:00Z", ProjectDirName: "proj"},
			"/projects/proj/sess-123/#msg-2024-01-15T10-00-00Z",
		},
		{
			"message without timestamp",
			ScanFinding{SourceType: "message", SourceID: "sess-123", ProjectDirName: "proj"},
			"/projects/proj/sess-123/",
		},
		{
			"message missing project",
			ScanFinding{SourceType: "message", SourceID: "sess-123"},
			"",
		},
		{
			"command with timestamp",
			ScanFinding{SourceType: "command", SourceID: "sess-123@2024-01-15T10:00:00Z", ProjectDirName: "proj"},
			"/projects/proj/sess-123/commands/#cmd-2024-01-15T10-00-00Z",
		},
		{
			"command without timestamp (old format, SourceID used as sessionID)",
			ScanFinding{SourceType: "command", SourceID: "42", ProjectDirName: "proj", SessionID: "sess-456"},
			"/projects/proj/42/commands/",
		},
		{
			"command without timestamp or project (fallback to SessionID)",
			ScanFinding{SourceType: "command", SourceID: "42", SessionID: "sess-456"},
			"",
		},
		{
			"plan with extension",
			ScanFinding{SourceType: "plan", SourceID: "my-plan.md"},
			"/plans/my-plan/",
		},
		{
			"plan without extension",
			ScanFinding{SourceType: "plan", SourceID: "my-plan"},
			"/plans/my-plan/",
		},
		{
			"shell_snapshot",
			ScanFinding{SourceType: "shell_snapshot", SourceID: "snap.sh"},
			"/shell-snapshots/snap/",
		},
		{
			"paste_cache",
			ScanFinding{SourceType: "paste_cache", SourceID: "clip.txt"},
			"/paste-cache/clip/",
		},
		{
			"memory",
			ScanFinding{SourceType: "memory", SourceID: "-Users-demo-proj"},
			"/memories/-Users-demo-proj/",
		},
		{
			"unknown type",
			ScanFinding{SourceType: "unknown"},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.finding.SourceURL()
			if got != tt.want {
				t.Errorf("SourceURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
