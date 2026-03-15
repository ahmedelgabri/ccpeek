package scan

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func setupTestDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal("opening store:", err)
	}
	t.Cleanup(func() { db.Close() })

	testdataDir := filepath.Join("..", "..", "testdata")
	if err := index.Run(context.Background(), testdataDir, db, true, io.Discard); err != nil {
		t.Fatal("index failed:", err)
	}
	return db
}

func TestNew(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	scanner, err := New(db)
	if err != nil {
		t.Fatal("New() failed:", err)
	}
	if scanner == nil {
		t.Fatal("expected non-nil scanner")
	}
}

func TestRunOnTestData(t *testing.T) {
	db := setupTestDB(t)

	scanner, err := New(db)
	if err != nil {
		t.Fatal("New() failed:", err)
	}

	findings, err := scanner.Run(context.Background())
	if err != nil {
		t.Fatal("Run() failed:", err)
	}

	// Testdata may or may not contain secrets; just verify it doesn't crash
	// and that findings are stored in the DB
	stored, err := db.ListScanFindings(context.Background(), "", "", true)
	if err != nil {
		t.Fatal("ListScanFindings failed:", err)
	}
	if len(stored) != len(findings) {
		t.Errorf("stored %d findings, but Run returned %d", len(stored), len(findings))
	}
}

func TestRunClearsPreviousFindings(t *testing.T) {
	db := setupTestDB(t)

	scanner, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	// Run twice — second run should replace, not accumulate
	_, err = scanner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstCount, _ := db.ScanFindingCount(context.Background())

	_, err = scanner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secondCount, _ := db.ScanFindingCount(context.Background())

	if firstCount != secondCount {
		t.Errorf("expected same count after re-scan, got %d then %d", firstCount, secondCount)
	}
}

func TestIgnoredFindingsSurviveRescan(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert a message with a detectable secret
	tx, err := db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := db.InsertProject(context.Background(), tx, "test-proj", "Test")
	sess := model.SessionEntry{
		SessionID: "s1", FirstPrompt: "test", MessageCount: 1,
		Created: "2025-01-01T00:00:00Z", Modified: "2025-01-01T00:00:00Z",
	}
	sessionDBID, _ := db.InsertSession(context.Background(), tx, projectID, sess, "")
	messages := []model.ConversationMessage{{
		Type: "human", Timestamp: "2025-01-01T00:00:00Z",
		Message: model.MessagePayload{
			Role:    "user",
			Content: []byte(`"key: AKIA2OGYBAH6QLHAMZXB"`),
		},
	}}
	db.InsertMessages(context.Background(), tx, sessionDBID, messages)
	tx.Commit()

	scanner, _ := New(db)

	// First scan
	findings, _ := scanner.Run(context.Background())
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}

	// Ignore the finding
	stored, _ := db.ListScanFindings(context.Background(), "", "", true)
	db.ToggleScanFindingIgnored(context.Background(), stored[0].ID)

	// Re-scan — ignored finding should persist, not duplicate
	scanner.Run(context.Background())

	all, _ := db.ListScanFindings(context.Background(), "", "", true)
	if len(all) != 1 {
		t.Errorf("expected 1 finding after re-scan (ignored persisted, no duplicate), got %d", len(all))
	}
	if !all[0].Ignored {
		t.Error("the surviving finding should still be ignored")
	}

	// Non-ignored view should be empty
	active, _ := db.ListScanFindings(context.Background(), "", "", false)
	if len(active) != 0 {
		t.Errorf("expected 0 active findings, got %d", len(active))
	}
}

func TestDetectKnownSecrets(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert a message with a known AWS key pattern
	tx, err := db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	projectID, err := db.InsertProject(context.Background(), tx, "test-project", "Test Project")
	if err != nil {
		t.Fatal(err)
	}

	sess := model.SessionEntry{
		SessionID:    "test-session-1",
		FirstPrompt:  "test",
		MessageCount: 1,
		Created:      "2025-01-01T00:00:00Z",
		Modified:     "2025-01-01T00:00:00Z",
	}
	sessionDBID, err := db.InsertSession(context.Background(), tx, projectID, sess, "")
	if err != nil {
		t.Fatal(err)
	}

	// Uses patterns that gitleaks actually detects (not well-known example keys)
	messages := []model.ConversationMessage{
		{
			Type:      "human",
			Timestamp: "2025-01-01T00:00:00Z",
			Message: model.MessagePayload{
				Role:    "user",
				Content: []byte(`"Here is my AWS key: AKIA2OGYBAH6QLHAMZXB and slack token: xoxb-123456789012-1234567890123-AbCdEfGhIjKlMnOpQrStUvWx"`),
			},
		},
	}
	if err := db.InsertMessages(context.Background(), tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	scanner, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	findings, err := scanner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(findings) < 2 {
		t.Fatalf("expected at least 2 findings (AWS + Slack), got %d", len(findings))
	}

	// Verify findings are redacted and have expected fields
	foundAWS := false
	foundSlack := false
	for _, f := range findings {
		if f.MatchRedacted == "" {
			t.Error("finding has empty redacted match")
		}
		if f.SourceType != "message" {
			t.Errorf("expected source_type 'message', got %q", f.SourceType)
		}
		if f.RuleID == "aws-access-token" {
			foundAWS = true
			// Redacted value should not contain the full key
			if f.MatchRedacted == "AKIA2OGYBAH6QLHAMZXB" {
				t.Error("AWS key should be redacted")
			}
		}
		if f.RuleID == "slack-bot-token" {
			foundSlack = true
		}
	}
	if !foundAWS {
		t.Error("expected AWS access token finding")
	}
	if !foundSlack {
		t.Error("expected Slack bot token finding")
	}
}

func TestRedact(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"ab", "**"},
		{"abcdef", "******"},
		{"abcdefghijklm", "ab*********lm"},
		{"AKIAIOSFODNN7EXAMPLE", "AKIA************MPLE"},
	}
	for _, tt := range tests {
		got := redact(tt.input)
		if got != tt.want {
			t.Errorf("redact(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestScanStats(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Empty DB should return zero stats
	stats, err := db.GetScanStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalFindings != 0 {
		t.Errorf("expected 0 findings, got %d", stats.TotalFindings)
	}

	// Insert a finding and check stats
	tx, err := db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.InsertScanFinding(context.Background(), tx, model.ScanFinding{
		RuleID:        "aws-access-key",
		Description:   "AWS Access Key",
		SourceType:    "message",
		SourceID:      "sess-1",
		MatchRedacted: "AKIA****MPLE",
		ScannedAt:     "2025-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	stats, err = db.GetScanStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalFindings != 1 {
		t.Errorf("expected 1 finding, got %d", stats.TotalFindings)
	}
	if stats.FindingsByRule["aws-access-key"] != 1 {
		t.Error("expected 1 aws-access-key finding in rule breakdown")
	}
	if stats.FindingsByType["message"] != 1 {
		t.Error("expected 1 message finding in type breakdown")
	}
}

func TestIgnoreToggle(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.InsertScanFinding(context.Background(), tx, model.ScanFinding{
		RuleID:        "test-rule",
		SourceType:    "message",
		SourceID:      "s1",
		MatchRedacted: "****",
		ScannedAt:     "2025-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Should be visible by default (not ignored)
	findings, _ := db.ListScanFindings(context.Background(), "", "", false)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Ignored {
		t.Error("finding should not be ignored initially")
	}

	// Toggle to ignored
	if err := db.ToggleScanFindingIgnored(context.Background(), findings[0].ID); err != nil {
		t.Fatal(err)
	}

	// Should be hidden when not showing ignored
	findings, _ = db.ListScanFindings(context.Background(), "", "", false)
	if len(findings) != 0 {
		t.Errorf("expected 0 non-ignored findings, got %d", len(findings))
	}

	// Should be visible when showing ignored
	findings, _ = db.ListScanFindings(context.Background(), "", "", true)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding with show_ignored, got %d", len(findings))
	}
	if !findings[0].Ignored {
		t.Error("finding should be ignored after toggle")
	}

	// Stats should exclude ignored findings
	stats, _ := db.GetScanStats(context.Background())
	if stats.TotalFindings != 0 {
		t.Errorf("expected 0 in stats (ignored), got %d", stats.TotalFindings)
	}

	// Toggle back to unignored
	if err := db.ToggleScanFindingIgnored(context.Background(), findings[0].ID); err != nil {
		t.Fatal(err)
	}
	findings, _ = db.ListScanFindings(context.Background(), "", "", false)
	if len(findings) != 1 || findings[0].Ignored {
		t.Error("finding should be unignored after second toggle")
	}
}

func TestSourceURL(t *testing.T) {
	tests := []struct {
		finding model.ScanFinding
		want    string
	}{
		{
			model.ScanFinding{SourceType: "message", SourceID: "sess-123", ProjectDirName: "proj-1"},
			"/projects/proj-1/sess-123/",
		},
		{
			model.ScanFinding{SourceType: "command", SessionID: "sess-123", ProjectDirName: "proj-1"},
			"/projects/proj-1/sess-123/commands/",
		},
		{
			model.ScanFinding{SourceType: "plan", SourceID: "my-plan.md"},
			"/plans/my-plan/",
		},
		{
			model.ScanFinding{SourceType: "shell_snapshot", SourceID: "snap.sh"},
			"/shell-snapshots/snap/",
		},
		{
			model.ScanFinding{SourceType: "paste_cache", SourceID: "clip.txt"},
			"/paste-cache/clip/",
		},
		{
			model.ScanFinding{SourceType: "memory", SourceID: "-Users-demo-proj"},
			"/memories/-Users-demo-proj/",
		},
		{
			model.ScanFinding{SourceType: "message", SourceID: "sess-123"},
			"",
		},
	}
	for _, tt := range tests {
		got := tt.finding.SourceURL()
		if got != tt.want {
			t.Errorf("SourceURL() for %s/%s = %q, want %q",
				tt.finding.SourceType, tt.finding.SourceID, got, tt.want)
		}
	}
}
