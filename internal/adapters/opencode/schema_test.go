package opencode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/agent/agenttest"
)

func TestNativeSchemaMustBeUsableBeforeSuppressingLegacyJSON(t *testing.T) {
	cases := []struct{ name, mutation string }{{name: "valid"}, {name: "empty sessions missing part", mutation: `DELETE FROM session; DROP TABLE part`}}
	for _, table := range []struct {
		name    string
		columns []string
	}{
		{"session", []string{"id", "title", "directory", "time_created", "time_updated", "parent_id"}},
		{"message", []string{"id", "session_id", "data"}},
		{"part", []string{"id", "session_id", "message_id", "data"}},
	} {
		cases = append(cases, struct{ name, mutation string }{table.name + " table", `DROP TABLE ` + table.name})
		for _, column := range table.columns {
			cases = append(cases, struct{ name, mutation string }{table.name + "." + column, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO missing", table.name, column)})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			root := agent.Root{Agent: Slug, Path: t.TempDir()}
			path := filepath.Join(root.Path, "opencode.db")
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			database.SetMaxOpenConns(1)
			// Keep schema changes in WAL as well as testing ordinary native rows.
			for _, q := range []string{
				`PRAGMA journal_mode=WAL`, `PRAGMA wal_autocheckpoint=0`,
				`CREATE TABLE session(id TEXT PRIMARY KEY,title TEXT,directory TEXT,time_created INTEGER,time_updated INTEGER,parent_id TEXT)`,
				`CREATE TABLE message(id TEXT PRIMARY KEY,session_id TEXT,data TEXT)`,
				`CREATE TABLE part(id TEXT PRIMARY KEY,session_id TEXT,message_id TEXT,data TEXT)`,
				`INSERT INTO session VALUES('shared','native','/project',1751443200000,1751443200000,NULL)`,
				`INSERT INTO message VALUES('msg','shared','{"role":"user"}')`,
				`INSERT INTO part VALUES('prt','shared','msg','{"type":"text","text":"native text"}')`,
			} {
				if _, err := database.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			if tc.mutation != "" {
				if _, err := database.Exec(tc.mutation); err != nil {
					t.Fatal(err)
				}
			}
			legacy := filepath.Join(root.Path, "storage", "session", "project", "shared.json")
			for _, file := range []struct{ path, body string }{
				{legacy, `{"id":"shared","title":"legacy"}`},
				{filepath.Join(root.Path, "storage", "message", "shared", "msg.json"), `{"id":"msg","role":"user","parts":[{"type":"text","text":"legacy text"}]}`},
			} {
				if err := os.MkdirAll(filepath.Dir(file.path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file.path, []byte(file.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a := New()
			refs, err := a.Discover(ctx, root)
			wantPath, wantText := path, "native text"
			if tc.mutation == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var incomplete *agent.IncompleteDiscovery
				if !errors.As(err, &incomplete) || len(incomplete.Issues) != 1 || incomplete.Issues[0].SourcePath != path {
					t.Fatalf("discovery diagnostic: %v", err)
				}
				wantPath, wantText = legacy, "legacy text"
			}
			if len(refs) != 1 || refs[0].Path != wantPath {
				t.Fatalf("refs=%+v want %s", refs, wantPath)
			}
			sink := &agenttest.Sink{}
			if err := a.Parse(ctx, refs[0], sink); err != nil {
				t.Fatal(err)
			}
			if len(sink.Messages) != 1 || sink.Messages[0].Text != wantText {
				t.Fatalf("messages=%+v want %q", sink.Messages, wantText)
			}
		})
	}
}
