package ingest

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

func newSinkStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// emitOneOfEach drives a sink through the records a source can produce.
func emitOneOfEach(t *testing.T, sink *dbSink) {
	t.Helper()
	if err := sink.Session(canon.Session{ExternalID: "s1", SourcePath: "/x.jsonl"}); err != nil {
		t.Fatalf("Session: %v", err)
	}
	if err := sink.Message(canon.Message{
		SessionExternalID: "s1", Seq: 0, Role: canon.RoleUser, Text: "hello",
	}); err != nil {
		t.Fatalf("Message: %v", err)
	}
	if err := sink.ToolCall(canon.ToolCall{
		SessionExternalID: "s1", Seq: 0, Name: "Bash",
	}); err != nil {
		t.Fatalf("ToolCall: %v", err)
	}
	if err := sink.Artifact(canon.Artifact{
		Kind: canon.ArtifactPlan, Name: "p.md", Content: "plan", SourcePath: "/p.md",
	}); err != nil {
		t.Fatalf("Artifact: %v", err)
	}
	if err := sink.History(canon.HistoryEntry{Display: "ls"}); err != nil {
		t.Fatalf("History: %v", err)
	}
	if err := sink.Issue(canon.Issue{
		Severity: canon.SeverityWarn, Category: "parse", Detail: "bad line",
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
}

// The sink stages its counts. Nothing reaches the run report until the
// transaction that produced them commits — records_indexed and the
// startup summary claim committed rows, and a rolled-back attempt
// (a tail parse that has to fall back, a parse that dies partway) wrote
// none.
func TestSinkCountsStayStagedUntilCommit(t *testing.T) {
	store := newSinkStore(t)
	w, err := store.BeginWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	report := &Report{}
	sink := newSink(w, "claude-code", "/x.jsonl", "hash", false)
	emitOneOfEach(t, sink)

	if report.Sessions != 0 || report.Messages != 0 || report.ToolCalls != 0 ||
		report.Artifacts != 0 || report.History != 0 || len(report.Issues) != 0 {
		t.Fatalf("run report was written to before commit: %+v", report)
	}
	if sink.report.Sessions != 1 || sink.report.Messages != 1 || sink.report.ToolCalls != 1 ||
		sink.report.Artifacts != 1 || sink.report.History != 1 || len(sink.report.Issues) != 1 {
		t.Fatalf("staged counts wrong: %+v", sink.report)
	}

	if err := w.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	// A rolled-back sink is simply never published.
	if report.Sessions != 0 || report.Messages != 0 {
		t.Errorf("rolled-back records reached the report: %+v", report)
	}
}

// commitTo is the only path from staged to published, and it carries
// every category across exactly once.
func TestSinkCommitToPublishesEverythingOnce(t *testing.T) {
	store := newSinkStore(t)
	w, err := store.BeginWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	report := &Report{}
	sink := newSink(w, "claude-code", "/x.jsonl", "hash", false)
	emitOneOfEach(t, sink)
	if err := w.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	sink.commitTo(report)

	if report.Sessions != 1 || report.Messages != 1 || report.ToolCalls != 1 ||
		report.Artifacts != 1 || report.History != 1 {
		t.Errorf("published counts = %+v, want one of each", report)
	}
	if len(report.Issues) != 1 {
		t.Errorf("published issues = %d, want 1", len(report.Issues))
	}
}

// A failed parse with no retry behind it keeps its diagnostics — those
// are what make the failure debuggable — but contributes no counts.
func TestSinkPublishIssuesKeepsDiagnosticsWithoutCounts(t *testing.T) {
	store := newSinkStore(t)
	w, err := store.BeginWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Rollback()

	report := &Report{}
	sink := newSink(w, "claude-code", "/x.jsonl", "hash", false)
	emitOneOfEach(t, sink)
	sink.publishIssues(report)

	if len(report.Issues) != 1 {
		t.Errorf("issues = %d, want 1", len(report.Issues))
	}
	if report.Sessions != 0 || report.Messages != 0 || report.ToolCalls != 0 ||
		report.Artifacts != 0 || report.History != 0 {
		t.Errorf("counts leaked from a failed parse: %+v", report)
	}
}

// Two sinks over the same run accumulate — one source per transaction is
// the pipeline's shape, and the run total is their sum.
func TestSinkCommitsAccumulateAcrossSources(t *testing.T) {
	store := newSinkStore(t)
	report := &Report{}

	for i, id := range []string{"s1", "s2"} {
		w, err := store.BeginWrite(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sink := newSink(w, "claude-code", "/x.jsonl", "hash", false)
		if err := sink.Session(canon.Session{ExternalID: id, SourcePath: "/x.jsonl"}); err != nil {
			t.Fatal(err)
		}
		if err := sink.Message(canon.Message{
			SessionExternalID: id, Seq: 0, Role: canon.RoleUser, Text: "hi",
		}); err != nil {
			t.Fatal(err)
		}
		if err := w.Commit(); err != nil {
			t.Fatal(err)
		}
		sink.commitTo(report)
		if report.Sessions != i+1 {
			t.Fatalf("after %d sources sessions = %d", i+1, report.Sessions)
		}
	}
	if report.Messages != 2 {
		t.Errorf("messages = %d, want 2", report.Messages)
	}
}

// Artifact content is unbounded on disk — a usage report is a whole HTML
// document, a paste is whatever the user pasted — and it was stored whole
// in artifacts.content AND again in search_docs. One bound applies where
// every adapter's artifacts converge.
func TestSinkCapsArtifactContent(t *testing.T) {
	ctx := context.Background()
	store := newSinkStore(t)
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}

	report := &Report{}
	sink := newSink(w, "claude-code", "/paste-cache/big.txt", "hash", false)
	huge := strings.Repeat("secret-ish padding ", canon.ArtifactContentLimit/10)
	if len(huge) <= canon.ArtifactContentLimit {
		t.Fatalf("fixture is only %d bytes, expected it over the limit", len(huge))
	}
	if err := sink.Artifact(canon.Artifact{
		Kind: canon.ArtifactPaste, Name: "big.txt", Content: huge,
		SourcePath: "/paste-cache/big.txt",
	}); err != nil {
		t.Fatalf("Artifact: %v", err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	sink.commitTo(report)

	var stored string
	if err := store.ReadDB().QueryRowContext(ctx,
		`SELECT content FROM artifacts WHERE name = 'big.txt'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored) > canon.ArtifactContentLimit+len(canon.ArtifactTruncationMarker) {
		t.Errorf("stored %d bytes, over the %d limit", len(stored), canon.ArtifactContentLimit)
	}
	if !strings.HasSuffix(stored, canon.ArtifactTruncationMarker) {
		t.Error("truncated artifact carries no marker")
	}

	// The truncation is reported, not silent.
	if len(report.Issues) != 1 {
		t.Fatalf("issues = %d, want 1 truncation warning", len(report.Issues))
	}
	if report.Issues[0].Category != "size" {
		t.Errorf("issue category = %q, want size", report.Issues[0].Category)
	}
	if report.Issues[0].Severity != canon.SeverityWarn {
		t.Errorf("issue severity = %q, want warn", report.Issues[0].Severity)
	}
}

// Content that fits is stored verbatim and reports nothing.
func TestSinkLeavesSmallArtifactsAlone(t *testing.T) {
	ctx := context.Background()
	store := newSinkStore(t)
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	report := &Report{}
	sink := newSink(w, "claude-code", "/plans/p.md", "hash", false)
	body := "# Plan\n\nDo the thing."
	if err := sink.Artifact(canon.Artifact{
		Kind: canon.ArtifactPlan, Name: "p.md", Content: body, SourcePath: "/plans/p.md",
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	sink.commitTo(report)

	var stored string
	if err := store.ReadDB().QueryRowContext(ctx,
		`SELECT content FROM artifacts WHERE name = 'p.md'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != body {
		t.Errorf("content = %q, want it verbatim", stored)
	}
	if len(report.Issues) != 0 {
		t.Errorf("issues = %+v, want none", report.Issues)
	}
}

// Machine-generated kinds are indexed as artifacts but kept out of the
// full-text index, where they cost a second copy of every byte plus FTS
// tokens and return hits nobody searches for.
func TestSinkOnlyFullTextIndexesProseKinds(t *testing.T) {
	ctx := context.Background()
	store := newSinkStore(t)
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sink := newSink(w, "claude-code", "/x", "hash", false)

	kinds := []struct {
		kind       canon.ArtifactKind
		searchable bool
	}{
		{canon.ArtifactPlan, true},
		{canon.ArtifactMemory, true},
		{canon.ArtifactTodoList, true},
		{canon.ArtifactPaste, true},
		{canon.ArtifactUsageReport, false},
		{canon.ArtifactShellSnapshot, false},
		{canon.ArtifactFileHistory, false},
	}
	for _, k := range kinds {
		if err := sink.Artifact(canon.Artifact{
			Kind: k.kind, Name: string(k.kind) + "-artifact",
			Content: "distinctive" + string(k.kind), SourcePath: "/x",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	for _, k := range kinds {
		var n int
		if err := store.ReadDB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM search_docs WHERE doc_type = ?`, string(k.kind)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if k.searchable && n != 1 {
			t.Errorf("%s search docs = %d, want 1", k.kind, n)
		}
		if !k.searchable && n != 0 {
			t.Errorf("%s search docs = %d, want 0", k.kind, n)
		}
		// Either way the artifact itself is indexed and browsable.
		if err := store.ReadDB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM artifacts WHERE kind = ?`, string(k.kind)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("%s artifacts = %d, want 1", k.kind, n)
		}
	}
}
