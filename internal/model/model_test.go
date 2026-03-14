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

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
