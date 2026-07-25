package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
)

// BenchmarkParseTranscript walks a synthetic transcript of the shape that
// dominates a real history: alternating user turns, assistant turns that
// issue tool calls, and user turns carrying the tool results back. This is
// the hottest path in the indexer — every byte of every session file goes
// through it.
func BenchmarkParseTranscript(b *testing.B) {
	const turns = 2000
	root := b.TempDir()
	projDir := filepath.Join(root, "projects", "-home-u-bench")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(projDir, "11111111-2222-4333-8444-555555555555.jsonl")

	var sb strings.Builder
	for i := range turns {
		ts := fmt.Sprintf("2026-07-%02dT10:%02d:%02dZ", i%28+1, i%60, i%60)
		prompt := fmt.Sprintf("please refactor the handler in module %d and explain the tradeoffs", i)
		fmt.Fprintf(&sb, `{"type":"user","uuid":"u%d","timestamp":%q,"cwd":"/home/u/bench","gitBranch":"main","sessionId":"11111111-2222-4333-8444-555555555555","message":{"role":"user","content":%s}}`+"\n",
			i, ts, mustJSON(prompt))
		fmt.Fprintf(&sb, `{"type":"assistant","uuid":"a%d","parentUuid":"u%d","timestamp":%q,"cwd":"/home/u/bench","requestId":"req_%d","sessionId":"11111111-2222-4333-8444-555555555555","message":{"role":"assistant","id":"msg_%d","model":"claude-sonnet-5","usage":{"input_tokens":1200,"output_tokens":340,"cache_read_input_tokens":8000,"cache_creation_input_tokens":150},"content":[{"type":"text","text":%s},{"type":"tool_use","id":"toolu_%d","name":"Edit","input":{"file_path":"internal/handler/module_%d.go","old_string":"a","new_string":"b"}}]}}`+"\n",
			i, i, ts, i, i, mustJSON("Here is the plan: I will rewrite module "+fmt.Sprint(i)+" to split parsing from validation."), i, i)
		fmt.Fprintf(&sb, `{"type":"user","uuid":"r%d","parentUuid":"a%d","timestamp":%q,"sessionId":"11111111-2222-4333-8444-555555555555","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":%s}]}}`+"\n",
			i, i, ts, i, mustJSON("The file internal/handler/module_"+fmt.Sprint(i)+".go has been updated successfully."))
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		b.Fatal(err)
	}

	src := agent.SourceRef{
		Root: agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault},
		Path: path, Kind: agent.SourceFile,
	}
	a := New()
	ctx := context.Background()

	b.SetBytes(int64(sb.Len()))
	b.ReportAllocs()
	for b.Loop() {
		sink := &agenttest.Sink{}
		if err := a.Parse(ctx, src, sink); err != nil {
			b.Fatal(err)
		}
		if len(sink.Messages) != turns*3 {
			b.Fatalf("messages = %d, want %d", len(sink.Messages), turns*3)
		}
		if len(sink.ToolCalls) != turns {
			b.Fatalf("tool calls = %d, want %d", len(sink.ToolCalls), turns)
		}
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
