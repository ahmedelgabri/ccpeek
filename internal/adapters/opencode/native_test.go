package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
)

func TestDiscoveryCancellationIsNotAnIncompleteResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	refs, err := New().Discover(ctx, agent.Root{Agent: Slug, Path: t.TempDir()})
	if !errors.Is(err, context.Canceled) || len(refs) != 0 {
		t.Fatalf("canceled discovery: %+v %v", refs, err)
	}
}

func TestWithPartsPreservesUnknownFields(t *testing.T) {
	message := []byte(`{"role":"assistant","metadata":{"large":9007199254740993}}`)
	part := json.RawMessage(`{"type":"text","text":"answer","unknown":{"large":9007199254740993}}`)
	raw, err := withParts(message, "msg_1", "ses_1", []json.RawMessage{part})
	if err != nil {
		t.Fatal(err)
	}
	var merged struct {
		Metadata json.RawMessage   `json:"metadata"`
		Parts    []json.RawMessage `json:"parts"`
	}
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatal(err)
	}
	if string(merged.Metadata) != `{"large":9007199254740993}` || len(merged.Parts) != 1 || string(merged.Parts[0]) != string(part) {
		t.Fatalf("lost raw fields: %s", raw)
	}
	if _, err := withParts(message, "msg_1", "ses_1", []json.RawMessage{json.RawMessage(`{"type":`)}); err == nil {
		t.Fatal("malformed part accepted")
	}
}

func TestNativeSeparatePartStorage(t *testing.T) {
	root := agent.Root{Agent: Slug, Path: t.TempDir()}
	files := map[string]string{
		"storage/session/project/ses_1.json": `{"id":"ses_1","title":"Native JSON","directory":"/project","time":{"created":1751443200000}}`,
		"storage/message/ses_1/msg_1.json":   `{"id":"msg_1","sessionID":"ses_1","role":"assistant","customMetadata":"preserved"}`,
		"storage/part/msg_1/prt_1.json":      `{"type":"text","text":"native text","customField":"also preserved"}`,
		"storage/part/msg_1/prt_2.json":      `{"type":"tool","tool":"bash","state":{"status":"completed","input":{"command":"go test ./..."},"output":"done"}}`,
	}
	for path, content := range files {
		path = filepath.Join(root.Path, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := New()
	refs, err := a.Discover(context.Background(), root)
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs=%+v err=%v", refs, err)
	}
	if len(refs[0].CompanionPaths) != 2 {
		t.Fatal(refs[0])
	}
	sink := &agenttest.Sink{}
	if err := a.Parse(context.Background(), refs[0], sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Messages) != 1 || sink.Messages[0].Text != "native text" {
		t.Fatal(sink.Messages)
	}
	if !strings.Contains(string(sink.Messages[0].Content), "also preserved") {
		t.Fatal("lost raw part metadata")
	}
	if len(sink.ToolCalls) != 1 || sink.ToolCalls[0].Command != "go test ./..." {
		t.Fatal(sink.ToolCalls)
	}
}

func TestNativeSQLiteAndWAL(t *testing.T) {
	ctx := context.Background()
	root := agent.Root{Agent: Slug, Path: t.TempDir()}
	path := filepath.Join(root.Path, "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`,
		`CREATE TABLE session(id TEXT PRIMARY KEY,title TEXT,directory TEXT,time_created INTEGER,time_updated INTEGER,parent_id TEXT)`,
		`CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT,data TEXT)`,
		`CREATE TABLE part(id TEXT PRIMARY KEY,session_id TEXT,message_id TEXT,data TEXT)`,
		`INSERT INTO session VALUES('ses_1','SQLite session','/project',1751443200000,1751443200000,NULL)`,
		`INSERT INTO message VALUES('msg_1','ses_1','{"role":"user"}')`,
		`INSERT INTO part VALUES('prt_1','ses_1','msg_1','{"type":"text","text":"WAL text"}')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	a := New()
	refs, err := a.Discover(ctx, root)
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs=%+v err=%v", refs, err)
	}
	// An unrelated broken database must not discard the usable native source.
	if err := os.WriteFile(filepath.Join(root.Path, "opencode-broken.db"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	partialRefs, err := a.Discover(ctx, root)
	var partial *agent.IncompleteDiscovery
	if !errors.As(err, &partial) || len(partialRefs) != 1 || partialRefs[0].Path != path {
		t.Fatalf("partial discovery: %+v %v", partialRefs, err)
	}
	if refs[0].Kind != agent.SourceDatabase || len(refs[0].CompanionPaths) != 1 || refs[0].CompanionPaths[0] != path+"-wal" {
		t.Fatal(refs[0])
	}
	sink := &agenttest.Sink{}
	if err := a.Parse(ctx, refs[0], sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.Messages) != 1 || sink.Messages[0].Text != "WAL text" {
		t.Fatal(sink.Messages)
	}
	if _, err := db.ExecContext(ctx, `UPDATE part SET data='{"type":"text","text":"updated"}'`); err != nil {
		t.Fatal(err)
	}
	sink = &agenttest.Sink{}
	if err := a.Parse(ctx, refs[0], sink); err != nil {
		t.Fatal(err)
	}
	if sink.Messages[0].Text != "updated" {
		t.Fatal(sink.Messages)
	}
}
