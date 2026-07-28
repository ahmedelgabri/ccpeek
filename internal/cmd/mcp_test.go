package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
	"github.com/fsnotify/fsnotify"
)

// lockedBuffer collects a server's stderr from the serving goroutine and
// the background index goroutine at once.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// mcpConn drives a server over the stdio pair, one request at a time —
// the way a client does, and the only way to observe what the tools
// actually return.
type mcpConn struct {
	t   *testing.T
	in  *io.PipeWriter
	out *json.Decoder
	id  int
}

func (c *mcpConn) call(method string, params map[string]any) map[string]any {
	c.t.Helper()
	c.id++
	req := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		req["params"] = params
	}
	line, err := json.Marshal(req)
	if err != nil {
		c.t.Fatal(err)
	}
	if _, err := c.in.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("writing %s: %v", method, err)
	}
	var resp struct {
		Result map[string]any `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := c.out.Decode(&resp); err != nil {
		c.t.Fatalf("reading the reply to %s: %v", method, err)
	}
	if resp.Error != nil {
		c.t.Fatalf("%s failed: %s", method, resp.Error.Message)
	}
	return resp.Result
}

// tool calls a tool and returns its text content.
func (c *mcpConn) tool(name string) string {
	c.t.Helper()
	result := c.call("tools/call", map[string]any{"name": name, "arguments": map[string]any{}})
	content, _ := result["content"].([]any)
	var out strings.Builder
	for _, part := range content {
		if m, ok := part.(map[string]any); ok {
			text, _ := m["text"].(string)
			out.WriteString(text)
		}
	}
	return out.String()
}

// MCP clients keep a server alive for days, so an index frozen at
// startup serves a snapshot that ages for the whole connection: sessions
// written after the handshake never appear. The server watches the agent
// roots once the first pass is done, so they do — without a restart.
func TestMCPIndexStaysLiveForTheConnection(t *testing.T) {
	if w, err := fsnotify.NewWatcher(); err != nil {
		t.Skipf("fsnotify unavailable: %v", err)
	} else {
		w.Close()
	}

	claudeDir := t.TempDir()
	writeClaudeSession(t, claudeDir, "11111111-1111-1111-1111-111111111111", "indexed at startup")
	cmd := pinRoots(t, filepath.Join(t.TempDir(), "ccpeek.db"), claudeDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd.SetContext(ctx)

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	var stderr lockedBuffer
	served := make(chan error, 1)
	go func() {
		// A short debounce keeps the test's watch passes prompt; the
		// command itself takes ingest's default.
		served <- runMCP(cmd, stdinR, stdoutW, &stderr, 20*time.Millisecond)
	}()
	// A server that never answers must fail the test, not hang it.
	watchdog := time.AfterFunc(60*time.Second, func() {
		stdinW.CloseWithError(io.ErrClosedPipe)
		stdoutR.CloseWithError(io.ErrClosedPipe)
	})
	defer watchdog.Stop()

	conn := &mcpConn{t: t, in: stdinW, out: json.NewDecoder(stdoutR)}
	conn.call("initialize", nil)

	// The handshake is answered while the first pass runs — that is the
	// point of indexing in the background — so wait for the archive before
	// asking what changed after it. The wait polls the DATA, not only the
	// status flag: the flag answers false in the moment between the
	// handshake and the background goroutine reaching its first pass, and a
	// test that stopped there raced the bootstrap it means to wait for.
	deadline := time.Now().Add(45 * time.Second)
	for !strings.Contains(conn.tool("sessions"), "11111111-1111-1111-1111-111111111111") {
		if time.Now().After(deadline) {
			t.Fatalf("the startup session never reached the bootstrap index\nstderr: %s", stderr.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	// …and the pass then settles, which is what the flag is for.
	for strings.Contains(conn.tool("status"), `"indexing": true`) {
		if time.Now().After(deadline) {
			t.Fatal("the first pass never finished")
		}
		time.Sleep(50 * time.Millisecond)
	}

	const fresh = "22222222-2222-2222-2222-222222222222"
	writeClaudeSession(t, claudeDir, fresh, "written mid-connection")
	// Watch registration races the write only on the first attempt: any
	// later event in the same tree triggers a pass, and that pass indexes
	// every source it has not seen. Re-touching keeps events coming until
	// the session lands, so the test never depends on which one won.
	for !strings.Contains(conn.tool("sessions"), fresh) {
		if time.Now().After(deadline) {
			t.Fatalf("a session written mid-connection never became queryable\nstderr: %s", stderr.String())
		}
		time.Sleep(200 * time.Millisecond)
		writeClaudeSession(t, claudeDir, fresh, "written mid-connection")
	}

	stdinW.Close()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("runMCP returned %v, want nil on EOF", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("runMCP did not return after stdin closed")
	}
}

// A client that connects, probes, and disconnects sends EOF while the
// first pass is still running. Closing the store under that pass killed
// it with "sql: database is closed" — and on a first run the bootstrap
// never reached its migrated_at stamp, so the next start redid all of
// it. The background work is canceled and JOINED before the store closes.
func TestMCPEOFWaitsForBackgroundIndexing(t *testing.T) {
	claudeDir := t.TempDir()
	for _, id := range sessionIDs(60) {
		writeClaudeSession(t, claudeDir, id, "probe corpus")
	}
	cmd := pinRoots(t, filepath.Join(t.TempDir(), "ccpeek.db"), claudeDir)
	cmd.SetContext(context.Background())

	var stderr lockedBuffer
	done := make(chan error, 1)
	go func() {
		// Immediate EOF: the client is gone before the pass can finish.
		done <- runMCP(cmd, strings.NewReader(""), io.Discard, &stderr, 20*time.Millisecond)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runMCP returned %v, want nil on EOF", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("runMCP did not return after EOF — the background join deadlocked")
	}

	if log := stderr.String(); strings.Contains(log, "database is closed") {
		t.Errorf("the store closed under the running pass:\n%s", log)
	}
}

// sessionIDs builds n distinct session ids.
func sessionIDs(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("%08d-1111-1111-1111-111111111111", i))
	}
	return out
}

// The `status` tool's Indexing flag has to cover EVERY pass — the ones
// this package drives and the ones the watch loop drives itself — and
// both bracket exactly now: the pipeline reports its own pass through
// ingest.Options.OnPass, so nothing is inferred from progress events and
// a grace period any more.
func TestIndexStateCoversEveryPass(t *testing.T) {
	var s indexState
	if s.running() {
		t.Error("an idle server reports indexing")
	}

	inside := false
	if err := s.during(func() error {
		inside = s.running()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !inside {
		t.Error("a bracketed pass does not report indexing")
	}
	if s.running() {
		t.Error("the flag stayed set after the bracketed pass finished")
	}

	// A watch pass the pipeline drives: OnPass brackets it, so the flag is
	// set for exactly its duration — including a pass that changes nothing,
	// which announces no end of its own.
	s.pass(true)
	if !s.running() {
		t.Error("a watch pass in flight does not report indexing")
	}
	s.pass(false)
	if s.running() {
		t.Error("the flag stayed set after the watch pass finished")
	}
}

// The pipeline's bracket must fire around a REAL pass, not just in the
// unit above: ingest.Runner.Run owns it, so the watch loop's own passes
// carry it without the caller doing anything.
func TestIngestOnPassBracketsARealRun(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}

	var s indexState
	insideRun := false
	if _, err := ingest.New(store, table).Run(ctx, ingest.Options{
		Getenv: func(string) string { return "" },
		Home:   t.TempDir(),
		OnPass: func(running bool) {
			s.pass(running)
			if running {
				insideRun = s.running()
			}
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !insideRun {
		t.Error("OnPass(true) did not fire before the pass ran")
	}
	if s.running() {
		t.Error("OnPass(false) did not fire when the pass returned")
	}
}

// The help has to state the freshness contract clients rely on: the
// index is not a startup snapshot.
func TestMCPHelpDocumentsTheLiveIndex(t *testing.T) {
	long := strings.ToLower(mcpCmd.Long)
	for _, want := range []string{"watches", "without a restart"} {
		if !strings.Contains(long, want) {
			t.Errorf("`ccpeek mcp` help does not mention %q:\n%s", want, mcpCmd.Long)
		}
	}
}
