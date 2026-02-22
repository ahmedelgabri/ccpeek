package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccexplore/internal/model"
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
	if len(idx.Plans) != 1 {
		t.Errorf("expected 1 plan, got %d", len(idx.Plans))
	} else {
		if idx.Plans[0].Title != "Test Plan Title" {
			t.Errorf("expected plan title 'Test Plan Title', got %q", idx.Plans[0].Title)
		}
		if idx.Plans[0].FileName != "test-plan.md" {
			t.Errorf("expected plan fileName 'test-plan.md', got %q", idx.Plans[0].FileName)
		}
	}

	// Shell snapshots
	if len(idx.ShellSnapshots) != 1 {
		t.Errorf("expected 1 snapshot, got %d", len(idx.ShellSnapshots))
	} else {
		if idx.ShellSnapshots[0].Timestamp != 1700000000 {
			t.Errorf("expected timestamp 1700000000, got %d", idx.ShellSnapshots[0].Timestamp)
		}
	}

	// Todos -- empty todo should be excluded
	if len(idx.Todos) != 1 {
		t.Errorf("expected 1 todo (empty excluded), got %d", len(idx.Todos))
	} else {
		if idx.Todos[0].ItemCount != 3 {
			t.Errorf("expected 3 items, got %d", idx.Todos[0].ItemCount)
		}
		if idx.Todos[0].Statuses["completed"] != 1 {
			t.Errorf("expected 1 completed, got %d", idx.Todos[0].Statuses["completed"])
		}
	}

	// Projects
	if len(idx.Projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(idx.Projects))
	} else {
		p := idx.Projects[0]
		if p.SessionCount != 1 {
			t.Errorf("expected 1 session, got %d", p.SessionCount)
		}
		if p.Sessions[0].MessageCount != 4 {
			t.Errorf("expected 4 messages (progress filtered), got %d", p.Sessions[0].MessageCount)
		}
		if p.Sessions[0].FirstPrompt != "hello world" {
			t.Errorf("expected first prompt 'hello world', got %q", p.Sessions[0].FirstPrompt)
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
		if len(msgs) != 4 {
			t.Errorf("expected 4 messages in JSON, got %d", len(msgs))
		}

		// Verify relationship: session has linked todo and file history
		sess := p.Sessions[0]
		if sess.TodoFileName == "" {
			t.Error("expected session to have a linked TodoFileName")
		}
		if !sess.HasFileHistory {
			t.Error("expected session to have HasFileHistory=true")
		}
		if sess.BashCommandCount != 1 {
			t.Errorf("expected BashCommandCount=1, got %d", sess.BashCommandCount)
		}
		if len(sess.ToolUseCounts) == 0 {
			t.Error("expected ToolUseCounts to be populated")
		} else if sess.ToolUseCounts["Bash"] != 1 {
			t.Errorf("expected ToolUseCounts[Bash]=1, got %d", sess.ToolUseCounts["Bash"])
		}
	}

	// File history
	if len(idx.FileHistory) != 1 {
		t.Errorf("expected 1 file history entry, got %d", len(idx.FileHistory))
	} else {
		fh := idx.FileHistory[0]
		if fh.FileCount != 2 {
			t.Errorf("expected 2 files, got %d", fh.FileCount)
		}
		if fh.ProjectDir != "test-project" {
			t.Errorf("expected file history ProjectDir %q, got %q", "test-project", fh.ProjectDir)
		}
	}

	// Todos -- verify relationship fields
	if len(idx.Todos) == 1 {
		todo := idx.Todos[0]
		if todo.SessionID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
			t.Errorf("expected todo SessionID to be UUID, got %q", todo.SessionID)
		}
		if todo.ProjectDir != "test-project" {
			t.Errorf("expected todo ProjectDir %q, got %q", "test-project", todo.ProjectDir)
		}
	}

	// History
	if len(idx.History) != 3 {
		t.Errorf("expected 3 history entries, got %d", len(idx.History))
	} else {
		// Should be sorted newest first
		if idx.History[0].Display != "third conversation" {
			t.Errorf("expected first history entry 'third conversation', got %q", idx.History[0].Display)
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
