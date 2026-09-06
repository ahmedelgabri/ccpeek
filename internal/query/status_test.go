package query

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/secrets"
)

func TestArchiveStatusTracksScanGenerationAndRules(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	svc := New(store, table)
	status, err := svc.ArchiveStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !status.Scan.Pending || status.SchemaVersion == 0 {
		t.Fatal(status)
	}
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	id, err := w.UpsertSession(canon.Session{Agent: "codex", ExternalID: "s"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	token := "xoxb-3336494366" + "76-7992618528" + "69-clFJVVIaoJahpORboa3Ba2al"
	if err := w.WriteMessage(id, "codex", canon.Message{Seq: 0, Text: "token " + token, Role: canon.RoleUser}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	scanner, err := secrets.New(store)
	if err != nil {
		t.Fatal(err)
	}
	findings, _, err := scanner.Run(ctx)
	if err != nil || len(findings) != 1 {
		t.Fatalf("canonical text findings=%+v err=%v", findings, err)
	}
	status, err = svc.ArchiveStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Scan.Pending || status.Generation != 1 || status.Scan.Generation != 1 || !status.DerivedDirty || status.PendingSessions != 1 {
		t.Fatal(status)
	}
	if err := store.SetMeta(ctx, "scan_rules_fingerprint", "outdated"); err != nil {
		t.Fatal(err)
	}
	status, err = svc.ArchiveStatus(ctx)
	if err != nil || !status.Scan.Pending {
		t.Fatalf("rules not stale: %+v %v", status, err)
	}
}

func TestTranscriptDoesNotDependOnSearchDocuments(t *testing.T) {
	ctx := context.Background()
	store, table := newStore(t)
	svc := New(store, table)
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	id, err := w.UpsertSession(canon.Session{Agent: "codex", ExternalID: "s"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"private":"raw-only"}`)
	if err := w.WriteMessage(id, "codex", canon.Message{Seq: 0, Text: "canonical text", Content: raw, Role: canon.RoleUser}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DELETE FROM search_docs`); err != nil {
		t.Fatal(err)
	}
	messages, err := svc.Transcript(ctx, "codex", "s", TranscriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Text != "canonical text" || len(messages[0].Content) != 0 {
		t.Fatal(messages)
	}
	messages, err = svc.Transcript(ctx, "codex", "s", TranscriptOptions{Full: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(messages[0].Content) != string(raw) {
		t.Fatal(messages)
	}
}
