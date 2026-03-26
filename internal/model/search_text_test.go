package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessagePayloadSearchTextIncludesToolResultsAndInputs(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "hello"},
		{Type: "tool_result", Content: json.RawMessage(`"tool output"`)},
		{Type: "tool_use", Name: "Write", Input: json.RawMessage(`{"file_path":"/tmp/test.txt","content":"secret body"}`)},
	}
	content, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}

	msg := MessagePayload{Content: content}
	text := msg.SearchText()
	for _, want := range []string{"hello", "tool output", "/tmp/test.txt", "secret body"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected SearchText to contain %q, got %q", want, text)
		}
	}
}
