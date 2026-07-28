package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func sessionLine(uuid, text string) string {
	return fmt.Sprintf(`{"type":"user","uuid":"%s","sessionId":"55555555-aaaa-bbbb-cccc-555555555555","timestamp":"2026-07-01T10:00:00Z","message":{"role":"user","content":"%s"}}`,
		uuid, text)
}

// TestParseSkipsOversizedLine: one line past maxLineBytes must cost
// exactly that record — every surrounding valid line still parses, the
// skip is a diagnostic, and the returned cursor covers the WHOLE file
// so a later append resumes cleanly past the oversized bytes.
func TestParseSkipsOversizedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "projects", "p", "55555555-aaaa-bbbb-cccc-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// A VALID JSON line bigger than the ceiling: size must not abort it.
	huge := sessionLine("u-huge", strings.Repeat("x", maxLineBytes))
	content := sessionLine("u-1", "before") + "\n" + huge + "\n" + sessionLine("u-2", "after") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src := agent.SourceRef{Root: agent.Root{Path: dir}, Path: path, Kind: agent.SourceFile}

	sink := &agenttest.Sink{}
	state, err := New().ParseTail(context.Background(), src, agent.TailState{}, sink)
	if err != nil {
		t.Fatalf("ParseTail: %v", err)
	}
	if len(sink.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (the valid ones around the oversized line)", len(sink.Messages))
	}
	if sink.Messages[0].ExternalID != "u-1" || sink.Messages[1].ExternalID != "u-2" {
		t.Errorf("messages = %s, %s", sink.Messages[0].ExternalID, sink.Messages[1].ExternalID)
	}
	found := false
	for _, is := range sink.Issues {
		if strings.Contains(is.Detail, "oversized") {
			found = true
		}
	}
	if !found {
		t.Errorf("no oversized-line diagnostic in %+v", sink.Issues)
	}
	fi, _ := os.Stat(path)
	if state.Offset != fi.Size() {
		t.Fatalf("cursor offset = %d, want %d (the skip must advance the cursor)", state.Offset, fi.Size())
	}

	// Append one more valid line: the tail parse must resume from the
	// cursor (prefix hash spans the skipped bytes) and emit only it.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(sessionLine("u-3", "appended") + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	tail := &agenttest.Sink{}
	state2, err := New().ParseTail(context.Background(), src, state, tail)
	if err != nil {
		t.Fatalf("tail ParseTail: %v", err)
	}
	if len(tail.Messages) != 1 || tail.Messages[0].ExternalID != "u-3" {
		t.Fatalf("tail messages = %+v, want just u-3", tail.Messages)
	}
	fi, _ = os.Stat(path)
	if state2.Offset != fi.Size() {
		t.Errorf("tail cursor = %d, want %d", state2.Offset, fi.Size())
	}
}

// discardSink drops every record: the benchmark measures the parser,
// not an accumulating test sink.
type discardSink struct{}

func (discardSink) Session(canon.Session) error                 { return nil }
func (discardSink) SessionRelation(canon.SessionRelation) error { return nil }
func (discardSink) Message(canon.Message) error                 { return nil }
func (discardSink) ToolCall(canon.ToolCall) error               { return nil }
func (discardSink) ToolResult(canon.ToolResult) error           { return nil }
func (discardSink) Artifact(canon.Artifact) error               { return nil }
func (discardSink) ArtifactLink(canon.ArtifactLink) error       { return nil }
func (discardSink) History(canon.HistoryEntry) error            { return nil }
func (discardSink) Issue(canon.Issue) error                     { return nil }

// BenchmarkParseLargeSession pins the streaming property: allocations
// per parsed message stay flat regardless of session length (the old
// implementation accumulated every message and tool call in slices
// before emitting).
func BenchmarkParseLargeSession(b *testing.B) {
	dir := b.TempDir()
	path := filepath.Join(dir, "projects", "p", "55555555-aaaa-bbbb-cccc-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	var sb strings.Builder
	for i := 0; i < 20_000; i++ {
		sb.WriteString(sessionLine(fmt.Sprintf("u-%06d", i), strings.Repeat("m", 512)))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	src := agent.SourceRef{Root: agent.Root{Path: dir}, Path: path, Kind: agent.SourceFile}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := New().Parse(context.Background(), src, discardSink{}); err != nil {
			b.Fatal(err)
		}
	}
}
