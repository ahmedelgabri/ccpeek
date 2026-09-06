package opencode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func assertForkRelation(t *testing.T, sink *agenttest.Sink) {
	t.Helper()
	if len(sink.Relations) != 1 {
		t.Fatalf("relations=%+v", sink.Relations)
	}
	rel := sink.Relations[0]
	if rel.Agent != Slug || rel.Kind != canon.RelForkOf || rel.FromExternalID != "ses_1" || rel.ToExternalID != "ses_parent" {
		t.Fatalf("fork direction/kind: %+v", rel)
	}
}

func TestToolResultExcerptKeepsCallIDAndFullRawOutput(t *testing.T) {
	output := strings.Repeat("result 🧪\n", canon.ToolResultExcerptLimit)
	raw, err := json.Marshal(map[string]any{"role": "assistant", "parts": []any{map[string]any{
		"type": "tool", "tool": "bash", "callID": "call_long", "state": map[string]any{"status": "completed", "input": map[string]string{"command": "synthetic"}, "output": output},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	sink := &agenttest.Sink{}
	if err := emitSession(context.Background(), "synthetic", sessionDoc{ID: "ses_1"}, []json.RawMessage{raw}, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.ToolCalls) != 1 || len(sink.Messages) != 1 {
		t.Fatalf("calls/messages: %+v", sink)
	}
	call := sink.ToolCalls[0]
	if call.ExternalID != "call_long" || call.SessionExternalID != "ses_1" || call.MessageSeq != sink.Messages[0].Seq {
		t.Fatalf("call linkage: %+v", call)
	}
	if len(call.ResultExcerpt) == 0 || len(call.ResultExcerpt) > canon.ToolResultExcerptLimit || !utf8.ValidString(call.ResultExcerpt) || !strings.HasPrefix(output, call.ResultExcerpt) {
		t.Fatalf("invalid excerpt: %q", call.ResultExcerpt)
	}
	var saved struct {
		Parts []struct {
			State struct {
				Output string `json:"output"`
			} `json:"state"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(sink.Messages[0].Content, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Parts) != 1 || saved.Parts[0].State.Output != output {
		t.Fatal("full raw output was truncated")
	}
}
