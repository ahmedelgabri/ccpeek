package model

import (
	"encoding/json"
	"os"
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
