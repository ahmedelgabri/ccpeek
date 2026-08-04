package cursor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func TestDiscoverIncludesTranscriptsAndSkipsStoreOverlap(t *testing.T) {
	root := t.TempDir()
	storeID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	jsonlOnly := "11111111-2222-3333-4444-555555555555"
	overlapID := storeID

	// store.db session
	storeDir := filepath.Join(root, "chats", "ws1", storeID)
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.db"), []byte("not-a-real-db"), 0o644); err != nil {
		t.Fatal(err)
	}

	// overlapping JSONL (same UUID as store) — must be skipped
	overlapDir := filepath.Join(root, "projects", "Users-test-proj", "agent-transcripts", overlapID)
	if err := os.MkdirAll(overlapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlapDir, overlapID+".jsonl"), []byte(
		`{"role":"user","message":{"content":[{"type":"text","text":"overlap"}]}}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	// JSONL-only session
	onlyDir := filepath.Join(root, "projects", "Users-test-proj", "agent-transcripts", jsonlOnly)
	if err := os.MkdirAll(onlyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onlyDir, jsonlOnly+".jsonl"), []byte(
		`{"role":"user","message":{"content":[{"type":"text","text":"<user_query>\nHello JSONL\n</user_query>"}]}}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	refs, err := New().Discover(context.Background(),
		agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range refs {
		paths = append(paths, r.Path)
	}
	if len(refs) != 2 {
		t.Fatalf("sources = %d (%v), want store.db + jsonl-only", len(refs), paths)
	}
	var sawStore, sawJSONL, sawOverlap bool
	for _, r := range refs {
		if strings.HasSuffix(r.Path, "store.db") {
			sawStore = true
			if r.Kind != agent.SourceDatabase {
				t.Errorf("store kind = %s", r.Kind)
			}
		}
		if strings.Contains(r.Path, jsonlOnly) {
			sawJSONL = true
			if r.Kind != agent.SourceFile {
				t.Errorf("jsonl kind = %s", r.Kind)
			}
		}
		if strings.Contains(r.Path, overlapID+".jsonl") {
			sawOverlap = true
		}
	}
	if !sawStore || !sawJSONL || sawOverlap {
		t.Fatalf("sawStore=%v sawJSONL=%v sawOverlap=%v paths=%v", sawStore, sawJSONL, sawOverlap, paths)
	}
}

func TestParseTranscriptJSONL(t *testing.T) {
	root := t.TempDir()
	sid := "abcdef01-2345-6789-abcd-ef0123456789"
	dir := filepath.Join(root, "projects", "Users-demo-api", "agent-transcripts", sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"role":"user","message":{"content":[{"type":"text","text":"<timestamp>Tuesday, Aug 4, 2026, 4:53 PM (UTC+2)</timestamp>\n<user_query>\nSpeed up CI\n</user_query>"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"On it."},{"type":"tool_use","name":"Read","input":{"path":"Makefile"}}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}`,
	}, "\n") + "\n"
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &agenttest.Sink{}
	src := agent.SourceRef{
		Root: agent.Root{Agent: Slug, Path: root},
		Path: path, Kind: agent.SourceFile,
	}
	if err := New().Parse(context.Background(), src, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(sink.Sessions))
	}
	sess := sink.Sessions[0]
	if sess.ExternalID != sid {
		t.Errorf("id = %q", sess.ExternalID)
	}
	if sess.Title != "Speed up CI" {
		t.Errorf("title = %q", sess.Title)
	}
	if sess.CWD != "/Users/demo/api" {
		t.Errorf("cwd = %q", sess.CWD)
	}
	if sess.CreatedAt.IsZero() || sess.ModifiedAt.IsZero() {
		t.Errorf("times unset: %v / %v", sess.CreatedAt, sess.ModifiedAt)
	}
	if len(sink.Messages) != 3 {
		t.Fatalf("messages = %d", len(sink.Messages))
	}
	roles := []canon.Role{sink.Messages[0].Role, sink.Messages[1].Role, sink.Messages[2].Role}
	want := []canon.Role{canon.RoleUser, canon.RoleAssistant, canon.RoleAssistant}
	if !slices.Equal(roles, want) {
		t.Fatalf("roles = %v", roles)
	}
	if len(sink.ToolCalls) != 1 || sink.ToolCalls[0].Name != "Read" {
		t.Fatalf("tools = %+v", sink.ToolCalls)
	}
}

func TestDecodeProjectDir(t *testing.T) {
	if got := decodeProjectDir("Users-demo-api"); got != "/Users/demo/api" {
		t.Errorf("got %q", got)
	}
	if got := decodeProjectDir("-Users-ahmed--dotfiles"); got != "/Users/ahmed/.dotfiles" {
		t.Errorf("got %q", got)
	}
}
