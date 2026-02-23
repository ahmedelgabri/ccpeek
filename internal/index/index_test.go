package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func TestRun(t *testing.T) {
	testdataDir := filepath.Join("..", "..", "testdata")
	outDir := t.TempDir()

	if err := Run(testdataDir, outDir); err != nil {
		t.Fatal("Run failed:", err)
	}

	// Verify index.json was written
	data, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		t.Fatal("reading index.json:", err)
	}

	var idx model.IndexData
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatal("parsing index.json:", err)
	}

	// Plans
	if len(idx.Plans) != 5 {
		t.Errorf("expected 5 plans, got %d", len(idx.Plans))
	}
	foundTestPlan := false
	for _, p := range idx.Plans {
		if p.FileName == "test-plan.md" {
			foundTestPlan = true
			if p.Title != "Test Plan Title" {
				t.Errorf("expected plan title 'Test Plan Title', got %q", p.Title)
			}
		}
	}
	if !foundTestPlan {
		t.Error("test-plan.md not found in plans")
	}

	// Shell snapshots (sorted newest first)
	if len(idx.ShellSnapshots) != 4 {
		t.Errorf("expected 4 snapshots, got %d", len(idx.ShellSnapshots))
	}
	foundOriginalSnapshot := false
	for _, s := range idx.ShellSnapshots {
		if s.Timestamp == 1700000000 {
			foundOriginalSnapshot = true
		}
	}
	if !foundOriginalSnapshot {
		t.Error("original snapshot (timestamp 1700000000) not found")
	}

	// Todos -- empty todo should be excluded
	if len(idx.Todos) != 4 {
		t.Errorf("expected 4 todos (empty excluded), got %d", len(idx.Todos))
	}
	foundOriginalTodo := false
	for _, td := range idx.Todos {
		if td.FileName == "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-11111111-2222-3333-4444-555555555555.json" {
			foundOriginalTodo = true
			if td.ItemCount != 3 {
				t.Errorf("expected 3 items in original todo, got %d", td.ItemCount)
			}
			if td.Statuses["completed"] != 1 {
				t.Errorf("expected 1 completed in original todo, got %d", td.Statuses["completed"])
			}
		}
	}
	if !foundOriginalTodo {
		t.Error("original todo not found")
	}

	// Projects (sorted by session count descending)
	if len(idx.Projects) != 5 {
		t.Errorf("expected 5 projects, got %d", len(idx.Projects))
	}
	var testProject *model.ProjectEntry
	for i := range idx.Projects {
		if idx.Projects[i].DirName == "test-project" {
			testProject = &idx.Projects[i]
			break
		}
	}
	if testProject == nil {
		t.Fatal("test-project not found in projects")
	}
	if testProject.SessionCount != 3 {
		t.Errorf("expected 3 sessions in test-project, got %d", testProject.SessionCount)
	}

	// Find the original session within test-project
	var origSession *model.SessionEntry
	for i := range testProject.Sessions {
		if testProject.Sessions[i].SessionID == "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
			origSession = &testProject.Sessions[i]
			break
		}
	}
	if origSession == nil {
		t.Fatal("original session not found in test-project")
	}
	if origSession.MessageCount != 5 {
		t.Errorf("expected 5 messages (progress filtered), got %d", origSession.MessageCount)
	}
	if origSession.FirstPrompt != "hello world" {
		t.Errorf("expected first prompt 'hello world', got %q", origSession.FirstPrompt)
	}

	// Verify session JSON was written
	sessData, err := os.ReadFile(filepath.Join(outDir, "projects", "test-project", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.json"))
	if err != nil {
		t.Fatal("reading session JSON:", err)
	}
	var msgs []model.ConversationMessage
	if err := json.Unmarshal(sessData, &msgs); err != nil {
		t.Fatal("parsing session JSON:", err)
	}
	if len(msgs) != 5 {
		t.Errorf("expected 5 messages in JSON, got %d", len(msgs))
	}

	// Verify relationship: session has linked todo and file history
	if origSession.TodoFileName == "" {
		t.Error("expected session to have a linked TodoFileName")
	}
	if !origSession.HasFileHistory {
		t.Error("expected session to have HasFileHistory=true")
	}
	if origSession.BashCommandCount != 1 {
		t.Errorf("expected BashCommandCount=1, got %d", origSession.BashCommandCount)
	}
	if len(origSession.ToolUseCounts) == 0 {
		t.Error("expected ToolUseCounts to be populated")
	} else {
		if origSession.ToolUseCounts["Bash"] != 1 {
			t.Errorf("expected ToolUseCounts[Bash]=1, got %d", origSession.ToolUseCounts["Bash"])
		}
		if origSession.ToolUseCounts["Write"] != 1 {
			t.Errorf("expected ToolUseCounts[Write]=1, got %d", origSession.ToolUseCounts["Write"])
		}
	}

	// File history (sorted by file count descending)
	if len(idx.FileHistory) != 3 {
		t.Errorf("expected 3 file history entries, got %d", len(idx.FileHistory))
	}
	foundOriginalFH := false
	for _, fh := range idx.FileHistory {
		if fh.ConversationID == "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
			foundOriginalFH = true
			if fh.FileCount != 2 {
				t.Errorf("expected 2 files in original file history, got %d", fh.FileCount)
			}
			if fh.ProjectDir != "test-project" {
				t.Errorf("expected file history ProjectDir %q, got %q", "test-project", fh.ProjectDir)
			}
		}
	}
	if !foundOriginalFH {
		t.Error("original file history entry not found")
	}

	// Todos -- verify relationship fields for original todo
	if foundOriginalTodo {
		for _, td := range idx.Todos {
			if td.FileName == "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-11111111-2222-3333-4444-555555555555.json" {
				if td.SessionID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
					t.Errorf("expected todo SessionID to be UUID, got %q", td.SessionID)
				}
				if td.ProjectDir != "test-project" {
					t.Errorf("expected todo ProjectDir %q, got %q", "test-project", td.ProjectDir)
				}
			}
		}
	}

	// History (sorted newest first)
	if len(idx.History) != 98 {
		t.Errorf("expected 98 history entries, got %d", len(idx.History))
	}
	if len(idx.History) > 0 {
		// Newest entry should be first (highest timestamp)
		if idx.History[0].Timestamp <= idx.History[len(idx.History)-1].Timestamp {
			t.Error("history should be sorted newest first")
		}
	}

	// Verify plan file was copied
	planContent, err := os.ReadFile(filepath.Join(outDir, "plans", "test-plan.md"))
	if err != nil {
		t.Fatal("plan file not copied:", err)
	}
	if len(planContent) == 0 {
		t.Error("plan file is empty")
	}

	// Verify snapshot file was copied
	snapContent, err := os.ReadFile(filepath.Join(outDir, "shell-snapshots", "snapshot-zsh-1700000000-abc123.sh"))
	if err != nil {
		t.Fatal("snapshot file not copied:", err)
	}
	if len(snapContent) == 0 {
		t.Error("snapshot file is empty")
	}
}

func TestDecodeProjectDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"-Users-ahmed--dotfiles", "/Users/ahmed/.dotfiles"},
		{"-Users-ahmed-code-personal-dev", "/Users/ahmed/code/personal/dev"},
		{"local-project", "local/project"},
	}

	for _, tt := range tests {
		got := decodeProjectDir(tt.input)
		if got != tt.want {
			t.Errorf("decodeProjectDir(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
