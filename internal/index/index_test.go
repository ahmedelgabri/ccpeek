package index

import (
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestRun(t *testing.T) {
	testdataDir := filepath.Join("..", "..", "testdata")

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal("opening store:", err)
	}
	defer s.Close()

	if err := Run(testdataDir, s); err != nil {
		t.Fatal("Run failed:", err)
	}

	// Plans
	plans, err := s.ListPlans()
	if err != nil {
		t.Fatal("listing plans:", err)
	}
	if len(plans) != 5 {
		t.Errorf("expected 5 plans, got %d", len(plans))
	}
	foundTestPlan := false
	for _, p := range plans {
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
	snapshots, err := s.ListShellSnapshots()
	if err != nil {
		t.Fatal("listing snapshots:", err)
	}
	if len(snapshots) != 4 {
		t.Errorf("expected 4 snapshots, got %d", len(snapshots))
	}
	foundOriginalSnapshot := false
	for _, sn := range snapshots {
		if sn.Timestamp == 1700000000 {
			foundOriginalSnapshot = true
		}
	}
	if !foundOriginalSnapshot {
		t.Error("original snapshot (timestamp 1700000000) not found")
	}

	// Todos -- empty todo should be excluded
	todos, err := s.ListTodos()
	if err != nil {
		t.Fatal("listing todos:", err)
	}
	if len(todos) != 4 {
		t.Errorf("expected 4 todos (empty excluded), got %d", len(todos))
	}
	foundOriginalTodo := false
	for _, td := range todos {
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
	projects, err := s.ListProjects()
	if err != nil {
		t.Fatal("listing projects:", err)
	}
	if len(projects) != 5 {
		t.Errorf("expected 5 projects, got %d", len(projects))
	}
	var testProject *struct {
		idx int
	}
	for i := range projects {
		if projects[i].DirName == "test-project" {
			testProject = &struct{ idx int }{i}
			break
		}
	}
	if testProject == nil {
		t.Fatal("test-project not found in projects")
	}
	project := projects[testProject.idx]
	if project.SessionCount != 3 {
		t.Errorf("expected 3 sessions in test-project, got %d", project.SessionCount)
	}

	// Find the original session
	var origSession *struct {
		idx int
	}
	for i := range project.Sessions {
		if project.Sessions[i].SessionID == "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
			origSession = &struct{ idx int }{i}
			break
		}
	}
	if origSession == nil {
		t.Fatal("original session not found in test-project")
	}
	sess := project.Sessions[origSession.idx]
	if sess.MessageCount != 5 {
		t.Errorf("expected 5 messages (progress filtered), got %d", sess.MessageCount)
	}
	if sess.FirstPrompt != "hello world" {
		t.Errorf("expected first prompt 'hello world', got %q", sess.FirstPrompt)
	}

	// Verify messages via store
	messages, total, err := s.GetSessionMessages("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", 0, 100)
	if err != nil {
		t.Fatal("getting session messages:", err)
	}
	if total != 5 {
		t.Errorf("expected 5 messages total, got %d", total)
	}
	if len(messages) != 5 {
		t.Errorf("expected 5 messages in page, got %d", len(messages))
	}

	// Verify relationship: session has linked todo and file history
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
	} else {
		if sess.ToolUseCounts["Bash"] != 1 {
			t.Errorf("expected ToolUseCounts[Bash]=1, got %d", sess.ToolUseCounts["Bash"])
		}
		if sess.ToolUseCounts["Write"] != 1 {
			t.Errorf("expected ToolUseCounts[Write]=1, got %d", sess.ToolUseCounts["Write"])
		}
	}

	// File history (sorted by file count descending)
	fileHistory, err := s.ListFileHistory()
	if err != nil {
		t.Fatal("listing file history:", err)
	}
	if len(fileHistory) != 3 {
		t.Errorf("expected 3 file history entries, got %d", len(fileHistory))
	}
	foundOriginalFH := false
	for _, fh := range fileHistory {
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

	// Todos -- verify relationship fields
	if foundOriginalTodo {
		for _, td := range todos {
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
	history, err := s.ListAllHistory()
	if err != nil {
		t.Fatal("listing history:", err)
	}
	if len(history) != 98 {
		t.Errorf("expected 98 history entries, got %d", len(history))
	}
	if len(history) > 0 {
		if history[0].Timestamp <= history[len(history)-1].Timestamp {
			t.Error("history should be sorted newest first")
		}
	}

	// Verify plan content
	_, planContent, err := s.GetPlan("test-plan")
	if err != nil {
		t.Fatal("getting plan content:", err)
	}
	if planContent == "" {
		t.Error("plan content is empty")
	}

	// Verify snapshot content
	_, snapContent, err := s.GetShellSnapshot("snapshot-zsh-1700000000-abc123")
	if err != nil {
		t.Fatal("getting snapshot content:", err)
	}
	if snapContent == "" {
		t.Error("snapshot content is empty")
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
