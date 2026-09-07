package ingest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/agent"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

type correctedUsageAdapter struct {
	agent.Adapter
	version int
	fail    bool
}

func (a *correctedUsageAdapter) ParseVersion() int { return a.version }
func (a *correctedUsageAdapter) Parse(ctx context.Context, src agent.SourceRef, sink agent.RecordSink) error {
	if err := a.Adapter.Parse(ctx, src, &correctedUsageSink{RecordSink: sink, version: a.version}); err != nil {
		return err
	}
	if a.fail {
		return fmt.Errorf("synthetic failed reparse")
	}
	return nil
}

type correctedUsageSink struct {
	agent.RecordSink
	version int
}

func (s *correctedUsageSink) Message(m canon.Message) error {
	if m.Usage != nil {
		u := *m.Usage
		u.OutputTokens = 100
		cost := 1.0
		u.ReportedCostUSD = &cost
		if s.version > 1 {
			u.InputTokens = 10
			u.OutputTokens = 40
			zero := 0.0
			u.ReportedCostUSD = &zero
		}
		m.Usage = &u
	}
	return s.RecordSink.Message(m)
}

func TestParserCorrectionPreservesUnavailableUsageClaims(t *testing.T) {
	runner, store := newRunner(t)
	adapter := &correctedUsageAdapter{Adapter: claude.New(), version: 1}
	runner.adapters = []agent.Adapter{adapter}
	root := t.TempDir()
	opts := isolatedOptions(root)
	owner := filepath.Join(root, "projects", "p", "a-owner.jsonl")
	available := filepath.Join(root, "projects", "p", "b-available.jsonl")
	retained := filepath.Join(root, "projects", "p", "c-retained.jsonl")
	writeSource(t, owner, usageLine(200))
	writeSource(t, available, usageLine(100))
	writeSource(t, retained, strings.ReplaceAll(usageLine(500), "content1", "irreplaceable"))
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{owner, retained} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	adapter.version = 2 // Same source bytes; a parser correction must still take effect.
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		if n := queryInt(t, store, `SELECT SUM(output_tokens) FROM message_usage`); n != 140 {
			t.Fatalf("usage output=%d", n)
		}
		if n := queryInt(t, store, `SELECT SUM(output_tokens) FROM rollup_usage_daily`); n != 140 {
			t.Fatalf("rollup output=%d", n)
		}
		if n := queryInt(t, store, `SELECT COUNT(*) FROM sessions`); n != 3 {
			t.Fatalf("retained sessions=%d", n)
		}
		if n := queryInt(t, store, `SELECT output_tokens FROM message_usage u JOIN messages m ON m.id=u.message_id WHERE m.content_id='irreplaceable'`); n != 100 {
			t.Fatalf("lost irreplaceable usage: %d", n)
		}
		if n := queryInt(t, store, `SELECT reported_cost_nanos FROM message_usage u JOIN messages m ON m.id=u.message_id WHERE m.content_id='content1'`); n != 0 {
			t.Fatalf("old reported cost survived correction: %d", n)
		}
		if n := queryInt(t, store, `SELECT json_extract(usage_json,'$.OutputTokens') FROM usage_claim_versions WHERE content_id='content1' AND parser_version=1`); n != 100 {
			t.Fatalf("lost prior interpretation: %d", n)
		}
		if path := queryString(t, store, `SELECT source_path FROM usage_claim_versions WHERE content_id='content1' AND parser_version=2`); path != available {
			t.Fatalf("provenance=%q", path)
		}
	}
	check()
	adapter.version = 3
	adapter.fail = true
	report, err := runner.Run(context.Background(), opts)
	if err != nil || report.Status != "partial" {
		t.Fatalf("failed pass: %+v %v", report, err)
	}
	if n := queryInt(t, store, `SELECT COUNT(*) FROM usage_claim_versions WHERE parser_version=3`); n != 0 {
		t.Fatalf("rolled-back correction persisted: %d", n)
	}
	check()
	adapter.version = 2
	adapter.fail = false
	opts.Rebuild = true
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	check()
	// An older parser cannot resurrect its incorrect maximum.
	adapter.version = 1
	opts.Rebuild = false
	if _, err := runner.Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	check()
}
