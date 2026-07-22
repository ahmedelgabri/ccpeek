package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/ops"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

func newServer(t *testing.T) *Server {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs("../../testdata/agents/claude-code")
	if err != nil {
		t.Fatal(err)
	}
	runner := ingest.New(store, table, claude.New())
	if _, err := runner.Run(context.Background(), ingest.Options{
		ConfigRoots: map[canon.AgentSlug][]string{claude.Slug: {fixtures}},
		Getenv:      func(string) string { return "" },
		Home:        "/nonexistent",
	}); err != nil {
		t.Fatal(err)
	}
	return New(query.New(store, table), "test")
}

// drive sends newline-delimited JSON-RPC requests and returns the decoded
// responses in order.
func drive(t *testing.T, s *Server, requests ...string) []map[string]any {
	t.Helper()
	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var responses []map[string]any
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 64*1024), 10*1024*1024)
	for sc.Scan() {
		var resp map[string]any
		if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
			t.Fatalf("bad response line: %v\n%s", err, sc.Text())
		}
		responses = append(responses, resp)
	}
	return responses
}

func TestInitializeAndListTools(t *testing.T) {
	s := newServer(t)
	resps := drive(
		t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	)
	// The notification must produce no response.
	if len(resps) != 2 {
		t.Fatalf("responses = %d, want 2", len(resps))
	}
	init := resps[0]["result"].(map[string]any)
	if init["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v", init["protocolVersion"])
	}
	tools := resps[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(ops.Registry()) {
		t.Fatalf("tools = %d, want %d (one per registry op)", len(tools), len(ops.Registry()))
	}
}

func TestToolCalls(t *testing.T) {
	s := newServer(t)
	resps := drive(
		t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"query":"rate limiting","limit":5}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"session","arguments":{"agent":"claude-code","id":"11111111-aaaa-bbbb-cccc-111111111111"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"session","arguments":{"agent":"claude-code","id":"missing"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"bogus/method"}`,
	)
	if len(resps) != 4 {
		t.Fatalf("responses = %d, want 4", len(resps))
	}

	text := func(i int) string {
		result := resps[i]["result"].(map[string]any)
		content := result["content"].([]any)[0].(map[string]any)
		return content["text"].(string)
	}
	if !strings.Contains(text(0), `"sessionId"`) {
		t.Errorf("search result lacks session ids: %s", text(0)[:200])
	}
	if !strings.Contains(text(1), "Add rate limiting to the login endpoint") {
		t.Errorf("session result lacks title")
	}

	// Missing session: tool-level error, not an RPC error.
	if resps[2]["error"] != nil {
		t.Error("not-found surfaced as RPC error, want isError result")
	}
	if resps[2]["result"].(map[string]any)["isError"] != true {
		t.Error("not-found result missing isError")
	}

	// Unknown method: RPC error.
	if resps[3]["error"] == nil {
		t.Error("unknown method must be an RPC error")
	}
}
