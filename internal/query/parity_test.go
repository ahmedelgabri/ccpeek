package query

import (
	"context"
	"strings"
	"testing"
)

func TestArtifactsListAndDetail(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	all, err := s.Artifacts(ctx, ArtifactsFilter{})
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	if len(all) != 9 {
		t.Fatalf("artifacts = %d, want 9", len(all))
	}

	plans, err := s.Artifacts(ctx, ArtifactsFilter{Kind: "plan"})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plan artifacts = %d (err %v), want 1", len(plans), err)
	}

	detail, err := s.Artifact(ctx, "claude-code", "plan", plans[0].Name,
		func(kind, content string) string {
			if kind == "plan" {
				return "<h1>rendered</h1>"
			}
			return ""
		})
	if err != nil {
		t.Fatalf("Artifact: %v", err)
	}
	if !strings.Contains(detail.Content, "Rate limiting rollout plan") {
		t.Errorf("content = %q", detail.Content[:40])
	}
	if detail.ContentHTML != "<h1>rendered</h1>" {
		t.Errorf("renderer not applied: %q", detail.ContentHTML)
	}

	// Linked artifact resolves its sessions.
	todo, err := s.Artifact(ctx, "claude-code", "todo_list",
		"11111111-aaaa-bbbb-cccc-111111111111-agent-99998888-aaaa-bbbb-cccc-777766665555.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(todo.SessionIDs) != 1 || todo.SessionIDs[0] != claudeSession1 {
		t.Errorf("todo sessions = %v", todo.SessionIDs)
	}
}

func TestScanFindingsToggle(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	// Seed one finding directly (the scanner package has its own tests).
	if _, err := s.store.DB().ExecContext(ctx, `
		INSERT INTO scan_findings (rule_id, description, entity_type, natural_key, match_redacted, line_number, scanned_at)
		VALUES ('slack-bot-token', 'Slack token', 'message', 'message/`+claudeSession1+`', 'xoxb…al', 3, '2026-07-10T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	findings, err := s.ScanFindings(ctx, false)
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings = %d (err %v)", len(findings), err)
	}

	if err := s.SetScanIgnore(ctx, findings[0].ID, true); err != nil {
		t.Fatalf("SetScanIgnore: %v", err)
	}
	visible, err := s.ScanFindings(ctx, false)
	if err != nil || len(visible) != 0 {
		t.Fatalf("visible after ignore = %d (err %v), want 0", len(visible), err)
	}
	all, err := s.ScanFindings(ctx, true)
	if err != nil || len(all) != 1 || !all[0].Ignored {
		t.Fatalf("all after ignore = %+v", all)
	}

	if err := s.SetScanIgnore(ctx, findings[0].ID, false); err != nil {
		t.Fatal(err)
	}
	visible, _ = s.ScanFindings(ctx, false)
	if len(visible) != 1 {
		t.Fatalf("visible after unignore = %d, want 1", len(visible))
	}
}

func TestBlocks(t *testing.T) {
	s := newService(t)
	blocks, err := s.Blocks(context.Background(), "", 50)
	if err != nil {
		t.Fatalf("Blocks: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("no blocks")
	}
	// Fixture timestamps span multiple 5h windows across 2026-07-01..05.
	var totalIn int64
	for _, b := range blocks {
		if b.Start >= b.End {
			t.Errorf("window %s..%s inverted", b.Start, b.End)
		}
		totalIn += b.Tokens.Input
	}
	if totalIn == 0 {
		t.Error("blocks carry no input tokens")
	}
	// The unpriced sidechain model must be surfaced, not silently priced.
	found := false
	for _, b := range blocks {
		if b.UnpricedTokens > 0 {
			found = true
		}
	}
	if !found {
		t.Error("no block reports unpriced tokens")
	}
}

func TestBudgetLifecycle(t *testing.T) {
	s := newService(t)
	ctx := context.Background()

	b, err := s.GetBudget(ctx)
	if err != nil {
		t.Fatalf("GetBudget: %v", err)
	}
	if b.MonthlyUSD != 0 {
		t.Errorf("default budget = %v, want 0", b.MonthlyUSD)
	}

	if err := s.SetBudget(ctx, 50); err != nil {
		t.Fatal(err)
	}
	b, err = s.GetBudget(ctx)
	if err != nil || b.MonthlyUSD != 50 {
		t.Fatalf("budget = %+v (err %v), want 50", b, err)
	}

	if err := s.SetBudget(ctx, 0); err != nil {
		t.Fatal(err)
	}
	b, _ = s.GetBudget(ctx)
	if b.MonthlyUSD != 0 {
		t.Errorf("cleared budget = %v", b.MonthlyUSD)
	}

	if err := s.SetBudget(ctx, -1); err == nil {
		t.Error("negative budget accepted")
	}
}
