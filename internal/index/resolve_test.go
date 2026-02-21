package index

import (
	"testing"

	"github.com/ahmedelgabri/claude-history/internal/model"
)

func TestTodoSessionRegex(t *testing.T) {
	tests := []struct {
		filename string
		wantID   string
		wantOK   bool
	}{
		{"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-11111111-2222-3333-4444-555555555555.json", "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", true},
		{"12345678-1234-1234-1234-123456789abc-agent-87654321-4321-4321-4321-cba987654321.json", "12345678-1234-1234-1234-123456789abc", true},
		{"not-a-uuid.json", "", false},
		{"plain-todo.json", "", false},
		{"", "", false},
	}

	for _, tt := range tests {
		m := todoSessionRe.FindStringSubmatch(tt.filename)
		if tt.wantOK {
			if m == nil {
				t.Errorf("expected match for %q", tt.filename)
				continue
			}
			if m[1] != tt.wantID {
				t.Errorf("filename %q: got session ID %q, want %q", tt.filename, m[1], tt.wantID)
			}
		} else {
			if m != nil {
				t.Errorf("expected no match for %q, got %v", tt.filename, m)
			}
		}
	}
}

func TestResolveRelationships_TodoToSession(t *testing.T) {
	idx := &model.IndexData{
		Projects: []model.ProjectEntry{
			{
				DirName:     "test-project",
				DisplayName: "test/project",
				Sessions: []model.SessionEntry{
					{SessionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
				},
			},
		},
		Todos: []model.TodoEntry{
			{FileName: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-11111111-2222-3333-4444-555555555555.json"},
		},
	}

	resolveRelationships(idx)

	todo := idx.Todos[0]
	if todo.SessionID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("todo SessionID = %q, want UUID", todo.SessionID)
	}
	if todo.ProjectDir != "test-project" {
		t.Errorf("todo ProjectDir = %q, want %q", todo.ProjectDir, "test-project")
	}
	if todo.ProjectName != "test/project" {
		t.Errorf("todo ProjectName = %q, want %q", todo.ProjectName, "test/project")
	}

	sess := idx.Projects[0].Sessions[0]
	if sess.TodoFileName != todo.FileName {
		t.Errorf("session TodoFileName = %q, want %q", sess.TodoFileName, todo.FileName)
	}
}

func TestResolveRelationships_FileHistoryToSession(t *testing.T) {
	idx := &model.IndexData{
		Projects: []model.ProjectEntry{
			{
				DirName:     "my-proj",
				DisplayName: "my/proj",
				Sessions: []model.SessionEntry{
					{SessionID: "11111111-2222-3333-4444-555555555555"},
				},
			},
		},
		FileHistory: []model.FileHistoryEntry{
			{ConversationID: "11111111-2222-3333-4444-555555555555", FileCount: 3},
		},
	}

	resolveRelationships(idx)

	fh := idx.FileHistory[0]
	if fh.ProjectDir != "my-proj" {
		t.Errorf("file history ProjectDir = %q, want %q", fh.ProjectDir, "my-proj")
	}
	if fh.ProjectName != "my/proj" {
		t.Errorf("file history ProjectName = %q, want %q", fh.ProjectName, "my/proj")
	}

	sess := idx.Projects[0].Sessions[0]
	if !sess.HasFileHistory {
		t.Error("session HasFileHistory should be true")
	}
}

func TestResolveRelationships_NoMatch(t *testing.T) {
	idx := &model.IndexData{
		Projects: []model.ProjectEntry{
			{
				DirName:     "test-project",
				DisplayName: "test/project",
				Sessions: []model.SessionEntry{
					{SessionID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
				},
			},
		},
		Todos: []model.TodoEntry{
			{FileName: "plain-todo.json"},
		},
		FileHistory: []model.FileHistoryEntry{
			{ConversationID: "unknown-id", FileCount: 1},
		},
	}

	resolveRelationships(idx)

	if idx.Todos[0].SessionID != "" {
		t.Errorf("non-UUID todo should have empty SessionID, got %q", idx.Todos[0].SessionID)
	}
	if idx.FileHistory[0].ProjectDir != "" {
		t.Errorf("unmatched file history should have empty ProjectDir, got %q", idx.FileHistory[0].ProjectDir)
	}
	if idx.Projects[0].Sessions[0].TodoFileName != "" {
		t.Errorf("session should have empty TodoFileName, got %q", idx.Projects[0].Sessions[0].TodoFileName)
	}
	if idx.Projects[0].Sessions[0].HasFileHistory {
		t.Error("session should not have HasFileHistory set")
	}
}
