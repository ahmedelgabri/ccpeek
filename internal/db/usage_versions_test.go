package db

import (
	"context"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

func TestUsageVersionMigrationRetainsUnknownProvenance(t *testing.T) {
	ctx := context.Background()
	store, path := openTemp(t)
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := w.UpsertSession(canon.Session{Agent: "pi", ExternalID: "owner", SourcePath: "/not-proof-of-original-source"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.InsertMessage(id, "pi", canon.Message{ContentID: "request", Usage: &canon.Usage{OutputTokens: 100}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	// Model a v17 ledger, whose current owner may have inherited another
	// source's richer observation. Migration must not invent source provenance.
	if _, err := store.DB().Exec(`DROP TABLE usage_claim_versions; UPDATE meta SET value='17' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version, output int
	var source string
	err = store.DB().QueryRow(`SELECT parser_version,source_path,json_extract(usage_json,'$.OutputTokens') FROM usage_claim_versions`).Scan(&version, &source, &output)
	if err != nil || version != 0 || source != "" || output != 100 {
		t.Fatalf("version=%d source=%q output=%d err=%v", version, source, output, err)
	}
	w, err = store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()
	w.UsageSource("/available", 1)
	if err := w.InsertMessage(id, "pi", canon.Message{Seq: 1, ContentID: "request", Usage: &canon.Usage{OutputTokens: 20}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, store, `SELECT SUM(output_tokens) FROM message_usage`); n != 20 {
		t.Fatalf("correction=%d", n)
	}
	if n := count(t, store, `SELECT json_extract(usage_json,'$.OutputTokens') FROM usage_claim_versions WHERE parser_version=0`); n != 100 {
		t.Fatalf("legacy claim lost: %d", n)
	}
}
