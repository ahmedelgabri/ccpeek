package secrets

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

func TestToolsAreScannedAndRulesChangesInvalidateState(t *testing.T) {
	for _, location := range []string{"command", "result"} {
		t.Run(location, func(t *testing.T) { testToolCoverage(t, location) })
	}
}

func testToolCoverage(t *testing.T, location string) {
	ctx := context.Background()
	store, err := db.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	id, err := w.UpsertSession(canon.Session{Agent: "codex", ExternalID: "s"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	token := "xoxb-3336494366" + "76-7992618528" + "69-clFJVVIaoJahpORboa3Ba2al"
	command, result := "echo ordinary", strings.Repeat("ordinary output\n", 1000)
	if location == "command" {
		command = "export SLACK_TOKEN=" + token
	} else {
		result += "token " + token
	}
	if err := w.InsertToolCall(id, canon.ToolCall{Seq: 0, MessageSeq: 3, Name: "shell", ExternalID: "call", Command: command}); err != nil {
		t.Fatal(err)
	}
	if err := w.UpdateToolCallResult(id, canon.ToolResult{CallExternalID: "call", Content: result}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	sc, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	findings, report, err := sc.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Line != 3 || report.SessionsScanned != 1 {
		t.Fatalf("findings=%+v report=%+v", findings, report)
	}
	_, report, err = sc.Run(ctx)
	if err != nil || report.SessionsScanned != 0 {
		t.Fatalf("warm scan=%+v %v", report, err)
	}
	sc.fingerprint = "new-rules-and-algorithm"
	_, report, err = sc.Run(ctx)
	if err != nil || report.SessionsScanned != 1 {
		t.Fatalf("rules refresh=%+v %v", report, err)
	}
}
