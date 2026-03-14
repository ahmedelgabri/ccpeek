package scan

import (
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func setupTestDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal("opening store:", err)
	}
	t.Cleanup(func() { db.Close() })

	testdataDir := filepath.Join("..", "..", "testdata")
	if err := index.Run(testdataDir, db, true); err != nil {
		t.Fatal("index failed:", err)
	}
	return db
}

func TestNew(t *testing.T) {
	db, err := store.Open(":memory:")
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

	findings, err := scanner.Run()
	if err != nil {
		t.Fatal("Run() failed:", err)
	}

	// Testdata may or may not contain secrets; just verify it doesn't crash
	// and that findings are stored in the DB
	stored, err := db.ListScanFindings("", "")
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
	_, err = scanner.Run()
	if err != nil {
		t.Fatal(err)
	}
	firstCount, _ := db.ScanFindingCount()

	_, err = scanner.Run()
	if err != nil {
		t.Fatal(err)
	}
	secondCount, _ := db.ScanFindingCount()

	if firstCount != secondCount {
		t.Errorf("expected same count after re-scan, got %d then %d", firstCount, secondCount)
	}
}

func TestDetectKnownSecrets(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Insert a message with a known AWS key pattern
	tx, err := db.BeginTx()
	if err != nil {
		t.Fatal(err)
	}

	projectID, err := db.InsertProject(tx, "test-project", "Test Project")
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
	sessionDBID, err := db.InsertSession(tx, projectID, sess, "")
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
	if err := db.InsertMessages(tx, sessionDBID, messages); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	scanner, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	findings, err := scanner.Run()
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
		{"abcdef", "ab**ef"},
		{"abcdefghijklm", "abcd*****jklm"},
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
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Empty DB should return zero stats
	stats, err := db.GetScanStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalFindings != 0 {
		t.Errorf("expected 0 findings, got %d", stats.TotalFindings)
	}

	// Insert a finding and check stats
	tx, err := db.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	err = db.InsertScanFinding(tx, model.ScanFinding{
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

	stats, err = db.GetScanStats()
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
