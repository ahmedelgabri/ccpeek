package index

import (
	"encoding/json"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func TestExtractCursorToolUseChangesApplyPatchPaths(t *testing.T) {
	patch := `*** Begin Patch
*** Add File: src/new_file.go
+package main
*** Update File: src/existing.go
@@
-old
+new
*** End Patch`

	changes := extractCursorToolUseChanges("ApplyPatch", map[string]any{
		"patch": patch,
	}, "2026-01-01T10:00:00Z")

	if len(changes) != 2 {
		t.Fatalf("expected two file changes from apply patch, got %d", len(changes))
	}
	if changes[0].ChangeKind != "patch" || changes[1].ChangeKind != "patch" {
		t.Fatalf("expected patch change kinds, got %+v", changes)
	}
	paths := map[string]bool{}
	for _, c := range changes {
		paths[c.FilePath] = true
	}
	if !paths["src/new_file.go"] || !paths["src/existing.go"] {
		t.Fatalf("expected parsed file paths from patch header, got %+v", paths)
	}
}

func TestExtractCursorToolUseChangesMultiEdit(t *testing.T) {
	changes := extractCursorToolUseChanges("MultiEdit", map[string]any{
		"file_path": "src/main.go",
		"edits": []any{
			map[string]any{"old_string": "old-a", "new_string": "new-a"},
			map[string]any{"old_string": "old-b", "new_string": ""},
		},
	}, "2026-01-01T10:00:00Z")

	if len(changes) != 2 {
		t.Fatalf("expected two multi-edit changes, got %d", len(changes))
	}
	if changes[0].FilePath != "src/main.go" || changes[1].FilePath != "src/main.go" {
		t.Fatalf("expected shared file path for multi-edit changes, got %+v", changes)
	}
	if changes[0].ChangeKind != "content" {
		t.Fatalf("expected first change kind content, got %q", changes[0].ChangeKind)
	}
	if changes[1].ChangeKind != "patch" {
		t.Fatalf("expected second change kind patch, got %q", changes[1].ChangeKind)
	}
}

func TestBuildCursorTranscriptFileHistoryFromToolUse(t *testing.T) {
	inputJSON, err := json.Marshal(map[string]any{
		"file_path": "src/write.go",
		"content":   "package main\nfunc main() {}",
	})
	if err != nil {
		t.Fatal(err)
	}
	contentBlocksJSON, err := json.Marshal([]model.ContentBlock{
		{
			Type:  "tool_use",
			Name:  "Write",
			Input: inputJSON,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	detail := buildCursorTranscriptFileHistory("session-1", []model.ConversationMessage{
		{
			Type:      "assistant",
			Timestamp: "2026-01-01T10:00:00Z",
			Message: model.MessagePayload{
				Role:    "assistant",
				Content: contentBlocksJSON,
			},
		},
	})

	if detail.ConversationID != "session-1" {
		t.Fatalf("expected conversation id session-1, got %q", detail.ConversationID)
	}
	if len(detail.Files) != 1 {
		t.Fatalf("expected one file version extracted from tool use, got %d", len(detail.Files))
	}
	if detail.Files[0].FilePath != "src/write.go" {
		t.Fatalf("expected file path src/write.go, got %q", detail.Files[0].FilePath)
	}
}
