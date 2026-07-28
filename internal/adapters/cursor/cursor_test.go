package cursor

import (
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

const sessionID = "e4f5a6b7-1111-2222-3333-cursor000001"

func parseFixture(t *testing.T) *agenttest.Sink {
	t.Helper()
	root, err := filepath.Abs("../../../testdata/agents/cursor")
	if err != nil {
		t.Fatal(err)
	}
	refs, err := New().Discover(context.Background(),
		agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("sources = %d, want 1", len(refs))
	}
	if refs[0].Kind != agent.SourceDatabase {
		t.Fatalf("kind = %q, want database", refs[0].Kind)
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), refs[0], sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sink
}

func TestParseStoreDB(t *testing.T) {
	sink := parseFixture(t)

	if len(sink.Sessions) != 1 {
		t.Fatalf("sessions = %d", len(sink.Sessions))
	}
	sess := sink.Sessions[0]
	if sess.ExternalID != sessionID {
		t.Errorf("external id = %q (must be the session-uuid dir)", sess.ExternalID)
	}
	if sess.Title != "Speed up the CI pipeline" {
		t.Errorf("title = %q (must come from hex meta)", sess.Title)
	}
	if sess.CWD != "/home/u/demo/api" {
		t.Errorf("cwd = %q", sess.CWD)
	}

	// 3 message blobs; the checkpoint blob is tolerated silently.
	if len(sink.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(sink.Messages))
	}
	if len(sink.Issues) != 0 {
		t.Errorf("issues = %v, want none", sink.Issues)
	}

	asst := sink.Messages[1]
	if asst.Role != canon.RoleAssistant || asst.Model != "claude-sonnet-5" {
		t.Errorf("assistant = %s/%s", asst.Role, asst.Model)
	}
	if asst.Usage == nil || asst.Usage.InputTokens != 1500 ||
		asst.Usage.CacheReadTokens != 6000 || asst.Usage.CacheWriteTokens != 250 {
		t.Errorf("usage = %+v", asst.Usage)
	}
	if asst.Text == "" {
		t.Error("assistant text not extracted from content blocks")
	}
	if sess.ModifiedAt.Before(sess.CreatedAt) {
		t.Errorf("times: %v / %v", sess.CreatedAt, sess.ModifiedAt)
	}
}

// buildStore writes a minimal store.db under a throwaway cursor root, with
// the blobs table's id column declared as idDecl and one message blob per
// id, and returns the root. Messages are numbered so their text names the
// id that carries them.
func buildStore(t *testing.T, idDecl string, ids []any) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "chats", "ws-hash", sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sdb, err := sql.Open("sqlite", filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sdb.Close()
	if _, err := sdb.Exec(`CREATE TABLE blobs (id ` + idDecl + ` PRIMARY KEY, data TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		// All user turns, so the folded title is unambiguously the FIRST
		// message in the order the parse settled on. Timestamps ascend with
		// the insert order rather than the id order, so a shuffled parse
		// also shows up in the session's created/modified times.
		blob := fmt.Sprintf(`{"role":"user","content":"msg %v","timestamp":%d}`,
			id, 1751364000000+int64(i)*1000)
		if _, err := sdb.Exec(`INSERT INTO blobs (id, data) VALUES (?, ?)`,
			id, hex.EncodeToString([]byte(blob))); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func parseRoot(t *testing.T, root string) *agenttest.Sink {
	t.Helper()
	refs, err := New().Discover(context.Background(),
		agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("sources = %d, want 1", len(refs))
	}
	sink := &agenttest.Sink{}
	if err := New().Parse(context.Background(), refs[0], sink); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return sink
}

// Blob ids are not always the zero-padded text the sample store uses: a
// real store.db can declare id INTEGER, or hold unpadded numeric text.
// Sorting those with Go's string compare orders them 1, 10, 11, 2 — which
// silently shuffles every message's seq, and with it the session title
// (first user turn) and created_at (first blob's timestamp).
func TestNumericBlobIDsOrderNumerically(t *testing.T) {
	// Deliberately inserted out of order, and past the point where string
	// and numeric compare diverge.
	unordered := []any{3, 11, 1, 2, 10, 4}
	for _, tt := range []struct {
		name   string
		decl   string
		ids    []any
		wantID []string
	}{
		{
			name: "integer column", decl: "INTEGER",
			ids:    unordered,
			wantID: []string{"1", "2", "3", "4", "10", "11"},
		},
		{
			name: "unpadded numeric text", decl: "TEXT",
			ids:    []any{"3", "11", "1", "2", "10", "4"},
			wantID: []string{"1", "2", "3", "4", "10", "11"},
		},
		{
			name: "non-numeric ids stay lexicographic", decl: "TEXT",
			ids:    []any{"blob-c", "blob-a", "blob-b"},
			wantID: []string{"blob-a", "blob-b", "blob-c"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sink := parseRoot(t, buildStore(t, tt.decl, tt.ids))

			var got []string
			for i, m := range sink.Messages {
				got = append(got, m.ExternalID)
				if m.Seq != i {
					t.Errorf("message %d carries seq %d", i, m.Seq)
				}
			}
			if !slices.Equal(got, tt.wantID) {
				t.Fatalf("message order = %v, want %v", got, tt.wantID)
			}

			// The session's own attributes are derived from that order.
			sess := sink.Sessions[len(sink.Sessions)-1]
			if want := "msg " + tt.wantID[0]; sess.Title != want {
				t.Errorf("title = %q, want %q (the FIRST user turn)", sess.Title, want)
			}
			if sess.CreatedAt.After(sess.ModifiedAt) {
				t.Errorf("created %v is after modified %v", sess.CreatedAt, sess.ModifiedAt)
			}
		})
	}
}

// A live cursor-agent keeps committed messages in store.db-wal until
// something checkpoints, so the wal has to be part of the source's
// fingerprint: without it the main file's size, mtime, and bytes never
// move and new messages are invisible to change detection — while Parse,
// which reads THROUGH the wal, sees them.
func TestDiscoverDeclaresWALCompanion(t *testing.T) {
	root := buildStore(t, "TEXT", []any{"1"})
	refs, err := New().Discover(context.Background(),
		agent.Root{Agent: Slug, Path: root, Origin: agent.RootFromDefault})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("sources = %d, want 1", len(refs))
	}
	want := []string{refs[0].Path + "-wal"}
	if !slices.Equal(refs[0].CompanionPaths, want) {
		t.Fatalf("companions = %v, want %v", refs[0].CompanionPaths, want)
	}
	// The wal is absent here (nothing has opened the store since it was
	// written) and that must be an ordinary state, not an error: the
	// pipeline folds a missing companion in as an "absent" marker.
	if _, err := os.Stat(want[0]); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly has a wal: %v", err)
	}
	sink := parseRoot(t, root)
	if len(sink.Messages) != 1 {
		t.Errorf("messages = %d with no wal present, want 1", len(sink.Messages))
	}
}
