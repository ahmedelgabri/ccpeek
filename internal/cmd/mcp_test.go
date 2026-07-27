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

	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
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

// newMCPCommand builds the flag set runMCP reads, with every agent root
// pinned inside the test's temp directories.
func newMCPCommand(t *testing.T, claudeDir string) *cobra.Command {
	t.Helper()
	cmd := pinRoots(t, filepath.Join(t.TempDir(), "ccpeek.db"), claudeDir)
	cmd.Flags().StringArray("root", nil, "")
	return cmd
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
	cmd := newMCPCommand(t, claudeDir)
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
	// point of indexing in the background — so wait for the archive to
	// settle before asking what changed after it.
	deadline := time.Now().Add(45 * time.Second)
	for strings.Contains(conn.tool("status"), `"indexing": true`) {
		if time.Now().After(deadline) {
			t.Fatal("the first pass never finished")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := conn.tool("sessions"); !strings.Contains(got, "11111111-1111-1111-1111-111111111111") {
		t.Fatalf("the startup session is missing from the bootstrap index: %s", got)
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
	cmd := newMCPCommand(t, claudeDir)
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

// The `status` tool's Indexing flag has to cover EVERY pass. Passes this
// package drives bracket exactly; watch passes report through progress
// events and lapse, because the watch loop announces only the end of a
// pass that changed something.
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

	// A watch pass in flight: its progress events hold the flag.
	s.progress(ingest.Progress{})
	if !s.running() {
		t.Error("a watch pass's progress event does not report indexing")
	}
	// …and it lapses instead of latching true forever, which is what a
	// pass that changed nothing (no end announcement) would otherwise do.
	s.lastEvent.Store(time.Now().Add(-2 * passGrace).UnixNano())
	if s.running() {
		t.Error("the flag latched true after the watch pass went quiet")
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
