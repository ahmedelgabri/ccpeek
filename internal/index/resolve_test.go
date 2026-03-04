package index

import (
	"testing"
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

// Relationship resolution tests (formerly TestResolveRelationships_*) are
// now covered by the integration test in index_test.go, which validates that
// todos and file history entries are correctly linked to sessions via the store.
