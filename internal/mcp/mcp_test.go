package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
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
	return New(query.New(store, table), "test", nil)
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

// TestStatusTool: with a status hook wired (the `ccpeek mcp` path,
// which now serves before indexing finishes), tools/list advertises the
// transport-owned status tool and calling it reports the index state —
// so a client can tell a warming archive from a settled one.
func TestStatusTool(t *testing.T) {
	s := newServer(t)
	s.status = func() Status {
		return Status{Indexing: true, V1ImportState: "failed", V1ImportError: "boom"}
	}
	resps := drive(
		t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"status","arguments":{}}}`,
	)
	if len(resps) != 2 {
		t.Fatalf("responses = %d, want 2", len(resps))
	}
	tools := resps[0]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(ops.Registry())+1 {
		t.Fatalf("tools = %d, want %d (registry + status)", len(tools), len(ops.Registry())+1)
	}
	result := resps[1]["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	for _, want := range []string{`"indexing": true`, `"v1ImportState": "failed"`, `"v1ImportError": "boom"`} {
		if !strings.Contains(text, want) {
			t.Errorf("status payload lacks %s: %s", want, text)
		}
	}
}

// An argument the tool does not declare is an ERROR, not a dropped
// filter. `search` given the plausible-but-wrong `agent_slug` used to
// search every agent and present the archive-wide hits as filtered; the
// caller had no way to tell. The message names the offender and the real
// arguments so a model can fix the call itself.
func TestUnknownArgumentsAreRejected(t *testing.T) {
	s := newServer(t)
	s.status = func() Status { return Status{} }
	resps := drive(
		t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search","arguments":{"query":"rate limiting","agent_slug":"pi"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sessions","arguments":{"q":"rate"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"status","arguments":{"verbose":true}}}`,
	)
	if len(resps) != 3 {
		t.Fatalf("responses = %d, want 3", len(resps))
	}
	for i, want := range []struct{ offender, valid string }{
		{`"agent_slug"`, "agent"},
		{`"q"`, "query"},
		{`"verbose"`, "takes no arguments"},
	} {
		result, ok := resps[i]["result"].(map[string]any)
		if !ok || result["isError"] != true {
			t.Errorf("call %d: unknown argument did not fail: %v", i, resps[i])
			continue
		}
		text := result["content"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.Contains(text, want.offender) {
			t.Errorf("call %d: error does not name %s: %s", i, want.offender, text)
		}
		if !strings.Contains(text, want.valid) {
			t.Errorf("call %d: error does not point at %q: %s", i, want.valid, text)
		}
	}
}

// Every advertised schema closes itself, so a client validating against
// it catches a misspelled argument before the call is even sent — the
// same rule call() enforces server-side.
func TestToolSchemasForbidUndeclaredArguments(t *testing.T) {
	s := newServer(t)
	s.status = func() Status { return Status{} }
	resps := drive(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := resps[0]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(ops.Registry())+1 {
		t.Fatalf("tools = %d, want %d (registry + status)", len(tools), len(ops.Registry())+1)
	}
	for _, tool := range tools {
		def := tool.(map[string]any)
		schema, ok := def["inputSchema"].(map[string]any)
		if !ok {
			t.Errorf("tool %v has no inputSchema", def["name"])
			continue
		}
		if schema["additionalProperties"] != false {
			t.Errorf("tool %v: additionalProperties = %v, want false",
				def["name"], schema["additionalProperties"])
		}
	}
}

// A tools/call with no params at all answered "invalid tool call params:
// unexpected end of JSON input" — the JSON decoder's complaint, useless
// to the caller. It must say what tools/call needs. A tool that takes no
// arguments still works without an "arguments" key.
func TestToolCallParamsMessages(t *testing.T) {
	s := newServer(t)
	resps := drive(
		t, s,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"stats"}}`,
	)
	if len(resps) != 3 {
		t.Fatalf("responses = %d, want 3", len(resps))
	}
	text := func(i int) string {
		result := resps[i]["result"].(map[string]any)
		return result["content"].([]any)[0].(map[string]any)["text"].(string)
	}

	if resps[0]["result"].(map[string]any)["isError"] != true {
		t.Error("tools/call without params must fail")
	}
	if strings.Contains(text(0), "unexpected end of JSON input") {
		t.Errorf("absent params still reports the decoder's error: %s", text(0))
	}
	if !strings.Contains(text(0), `"name"`) {
		t.Errorf("absent params does not say what is needed: %s", text(0))
	}
	if !strings.Contains(text(1), `"name"`) {
		t.Errorf("empty params does not name the missing field: %s", text(1))
	}

	// A zero-argument tool needs no "arguments" key.
	if result := resps[2]["result"].(map[string]any); result["isError"] == true {
		t.Errorf("stats without an arguments key failed: %s", text(2))
	} else if !strings.Contains(text(2), `"sessions"`) {
		t.Errorf("stats payload looks wrong: %s", text(2))
	}
}

// A parse error carries "id": null, as JSON-RPC 2.0 requires when the
// request's id cannot be determined. Omitting the field entirely — which
// omitempty did — makes strict clients reject the response.
func TestParseErrorCarriesNullID(t *testing.T) {
	srv := New(nil, "test", nil)
	var out bytes.Buffer
	if err := srv.Serve(context.Background(),
		strings.NewReader("{ this is not json\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("response is not JSON (%q): %v", out.String(), err)
	}
	id, present := raw["id"]
	if !present {
		t.Fatal("response has no id field; the spec requires an explicit null")
	}
	if string(id) != "null" {
		t.Errorf("id = %s, want null", id)
	}
	if _, ok := raw["error"]; !ok {
		t.Error("parse error response has no error member")
	}
}

// Every reply echoes its request's id, whatever JSON type it is.
func TestResponsesEchoTheRequestID(t *testing.T) {
	srv := New(nil, "test", nil)
	for _, id := range []string{`1`, `"abc"`, `0`} {
		var out bytes.Buffer
		in := `{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}` + "\n"
		if err := srv.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
			t.Fatalf("id %s: %v (%q)", id, err, out.String())
		}
		if string(raw["id"]) != id {
			t.Errorf("id = %s, want %s", raw["id"], id)
		}
		if _, ok := raw["result"]; !ok {
			t.Errorf("ping with id %s returned no result member", id)
		}
	}
}

// One oversized message costs that message, not the session: the whole
// MCP connection used to die with bufio.ErrTooLong.
func TestOversizedMessageDoesNotKillTheSession(t *testing.T) {
	srv := New(nil, "test", nil)
	var in strings.Builder
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"ping","params":{"pad":"` +
		strings.Repeat("x", maxMessageBytes+1024) + `"}}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":3,"method":"ping"}` + "\n")

	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Serve died on an oversized message: %v", err)
	}

	var ids []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatalf("bad response line %q: %v", line, err)
		}
		ids = append(ids, string(raw["id"]))
	}
	// Three replies: ping 1, the oversized message's error, ping 3.
	want := []string{"1", "null", "3"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("response ids = %v, want %v", ids, want)
	}
}

// Notifications (no id) get no reply at all.
func TestNotificationsAreSilent(t *testing.T) {
	srv := New(nil, "test", nil)
	var out bytes.Buffer
	in := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	if err := srv.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("notification produced %q, want no reply", out.String())
	}
}
