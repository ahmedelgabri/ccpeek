package pi

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

// BenchmarkParseSession walks a synthetic Pi session of the shape that
// dominates a real history: user turns, assistant turns whose content
// blocks issue tool calls, and the toolResult entries that answer them.
func BenchmarkParseSession(b *testing.B) {
	const turns = 2000
	root := b.TempDir()
	dir := filepath.Join(root, "sessions", "-home-u-bench")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "bench.jsonl")

	var sb strings.Builder
	sb.WriteString(`{"type":"session","id":"ses_bench","version":1,"cwd":"/home/u/bench","timestamp":"2026-07-01T09:00:00Z"}` + "\n")
	for i := range turns {
		ts := fmt.Sprintf("2026-07-%02dT10:%02d:%02dZ", i%28+1, i%60, i%60)
		fmt.Fprintf(&sb, `{"type":"message","id":"u%d","timestamp":%q,"message":{"role":"user","content":%s}}`+"\n",
			i, ts, mustJSONString(fmt.Sprintf("please refactor the handler in module %d and explain the tradeoffs", i)))
		fmt.Fprintf(&sb, `{"type":"message","id":"a%d","parentId":"u%d","timestamp":%q,"message":{"role":"assistant","usage":{"input":1200,"output":340,"cacheRead":8000,"cacheWrite":150,"cost":{"total":0.0091}},"content":[{"type":"text","text":%s},{"type":"toolCall","id":"call_%d","name":"edit","arguments":{"path":"internal/handler/module_%d.go","oldText":"a","newText":"b"}}]}}`+"\n",
			i, i, ts, mustJSONString("Here is the plan: I will rewrite module "+fmt.Sprint(i)+" to split parsing from validation."), i, i)
		fmt.Fprintf(&sb, `{"type":"message","id":"r%d","parentId":"a%d","timestamp":%q,"message":{"role":"toolResult","toolCallId":"call_%d","toolName":"edit","content":%s}}`+"\n",
			i, i, ts, i, mustJSONString("The file internal/handler/module_"+fmt.Sprint(i)+".go has been updated successfully."))
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

func mustJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
