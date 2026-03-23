package index

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

var applyPatchFileRe = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)

type cursorFileChange struct {
	FilePath   string
	Content    string
	Patch      string
	ChangeKind string
	Timestamp  string
}

func indexCursorFileHistory(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx) (int, error) {
	return indexCursorFileHistoryWithFilter(ctx, cursorDir, s, tx, nil)
}

func indexCursorFileHistoryFiltered(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
	return indexCursorFileHistoryWithFilter(ctx, cursorDir, s, tx, changed)
}

func indexCursorFileHistoryWithFilter(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, nil
	}
	projectsDir := filepath.Join(cursorDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirName := e.Name()
		transcriptsDir := filepath.Join(projectsDir, dirName, "agent-transcripts")
		items, err := os.ReadDir(transcriptsDir)
		if err != nil {
			continue
		}

		for _, item := range items {
			sessionID, jsonlPath, ok := cursorSessionPath(transcriptsDir, item)
			if !ok {
				continue
			}
			if changed != nil && !changed[jsonlPath] {
				continue
			}
			messages, err := parseCursorTranscript(jsonlPath, sessionID)
			if err != nil || len(messages) == 0 {
				continue
			}

			detail := buildCursorTranscriptFileHistory(sessionID, messages)
			if len(detail.Files) == 0 {
				continue
			}

			updatedAt := int64(0)
			if info, err := os.Stat(jsonlPath); err == nil {
				updatedAt = info.ModTime().UnixMilli()
			}

			sessionDBID := int64(0)
			if dbID, err := s.GetSessionDBIDForProject(ctx, tx, dirName, sessionID); err == nil {
				sessionDBID = dbID
				_ = s.LinkFileHistoryToSession(ctx, tx, sessionID, dbID)
			}

			if err := s.InsertFileHistoryWithMeta(ctx, tx, sessionID, detail.Files, sessionDBID, updatedAt, model.SourceCursor, jsonlPath); err != nil {
				continue
			}
			count++
		}
	}
	return count, nil
}

func buildCursorTranscriptFileHistory(sessionID string, messages []model.ConversationMessage) model.FileHistoryDetail {
	var changes []cursorFileChange

	for _, m := range messages {
		role := m.Message.Role
		if role == "" {
			role = m.Type
		}
		if role != "assistant" {
			continue
		}

		for _, b := range m.Message.ContentBlocks() {
			if b.Type != "tool_use" || b.Name == "" {
				continue
			}
			var input map[string]any
			if json.Unmarshal(b.Input, &input) != nil {
				continue
			}
			changes = append(changes, extractCursorToolUseChanges(b.Name, input, m.Timestamp)...)
		}
	}

	return model.FileHistoryDetail{
		ConversationID: sessionID,
		Files:          buildFileVersionsFromChanges(changes),
	}
}

func extractCursorToolUseChanges(toolName string, input map[string]any, ts string) []cursorFileChange {
	tool := strings.TrimSpace(toolName)
	switch tool {
	case "Write":
		filePath := normalizeArtifactPath(firstStringFromMap(input, "file_path", "filePath", "path"))
		if filePath == "" {
			return nil
		}
		content := truncateLarge(firstStringFromMap(input, "content"), 200_000)
		return []cursorFileChange{{
			FilePath:   filePath,
			Content:    content,
			ChangeKind: "content",
			Timestamp:  ts,
		}}
	case "Edit":
		filePath := normalizeArtifactPath(firstStringFromMap(input, "file_path", "filePath", "path"))
		if filePath == "" {
			return nil
		}
		oldStr := firstStringFromMap(input, "old_string", "oldString")
		newStr := firstStringFromMap(input, "new_string", "newString", "content")
		patch := buildSimplePatch(oldStr, newStr)
		if newStr != "" {
			return []cursorFileChange{{
				FilePath:   filePath,
				Content:    truncateLarge(newStr, 200_000),
				Patch:      truncateLarge(patch, 60_000),
				ChangeKind: "content",
				Timestamp:  ts,
			}}
		}
		return []cursorFileChange{{
			FilePath:   filePath,
			Patch:      truncateLarge(patch, 60_000),
			ChangeKind: "patch",
			Timestamp:  ts,
		}}
	case "MultiEdit":
		filePath := normalizeArtifactPath(firstStringFromMap(input, "file_path", "filePath", "path"))
		if filePath == "" {
			return nil
		}
		var out []cursorFileChange
		edits := asSlice(input["edits"])
		for _, e := range edits {
			em := asMap(e)
			if len(em) == 0 {
				continue
			}
			oldStr := firstStringFromMap(em, "old_string", "oldString")
			newStr := firstStringFromMap(em, "new_string", "newString")
			patch := buildSimplePatch(oldStr, newStr)
			change := cursorFileChange{
				FilePath:  filePath,
				Patch:     truncateLarge(patch, 60_000),
				Timestamp: ts,
			}
			if newStr != "" {
				change.Content = truncateLarge(newStr, 200_000)
				change.ChangeKind = "content"
			} else {
				change.ChangeKind = "patch"
			}
			out = append(out, change)
		}
		if len(out) == 0 {
			newStr := firstStringFromMap(input, "new_string", "newString", "content")
			oldStr := firstStringFromMap(input, "old_string", "oldString")
			patch := buildSimplePatch(oldStr, newStr)
			change := cursorFileChange{
				FilePath:  filePath,
				Patch:     truncateLarge(patch, 60_000),
				Timestamp: ts,
			}
			if newStr != "" {
				change.Content = truncateLarge(newStr, 200_000)
				change.ChangeKind = "content"
			} else {
				change.ChangeKind = "patch"
			}
			out = append(out, change)
		}
		return out
	case "ApplyPatch":
		patch := truncateLarge(firstStringFromMap(input, "patch", "diff", "content"), 120_000)
		if patch == "" {
			return nil
		}
		paths := parseApplyPatchPaths(patch)
		if len(paths) == 0 {
			if fp := normalizeArtifactPath(firstStringFromMap(input, "file_path", "filePath", "path")); fp != "" {
				paths = []string{fp}
			}
		}
		if len(paths) == 0 {
			paths = []string{"(patch)"}
		}
		out := make([]cursorFileChange, 0, len(paths))
		for _, p := range paths {
			out = append(out, cursorFileChange{
				FilePath:   normalizeArtifactPath(p),
				Patch:      patch,
				ChangeKind: "patch",
				Timestamp:  ts,
			})
		}
		return out
	}
	return nil
}

func parseApplyPatchPaths(patch string) []string {
	matches := applyPatchFileRe.FindAllStringSubmatch(patch, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool)
	var paths []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		p := strings.TrimSpace(m[1])
		p = strings.Trim(p, `"`)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		paths = append(paths, p)
	}
	return paths
}

func buildFileVersionsFromChanges(changes []cursorFileChange) []model.FileVersionInfo {
	versionByPath := make(map[string]int)
	files := make([]model.FileVersionInfo, 0, len(changes))

	for _, c := range changes {
		hash := normalizeArtifactPath(c.FilePath)
		if hash == "" {
			hash = "(unknown)"
		}
		versionByPath[hash]++

		kind := c.ChangeKind
		if kind == "" {
			if c.Content != "" {
				kind = "content"
			} else if c.Patch != "" {
				kind = "patch"
			} else {
				kind = "metadata"
			}
		}

		files = append(files, model.FileVersionInfo{
			Hash:       hash,
			Version:    versionByPath[hash],
			Content:    c.Content,
			FilePath:   hash,
			ChangeKind: kind,
			Patch:      c.Patch,
			Timestamp:  c.Timestamp,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Hash != files[j].Hash {
			return files[i].Hash < files[j].Hash
		}
		return files[i].Version < files[j].Version
	})
	return files
}

func firstStringFromMap(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := anyString(m[k]); s != "" {
			return s
		}
	}
	return ""
}

func asSlice(v any) []any {
	if arr, ok := v.([]any); ok {
		return arr
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case float64:
		return strings.TrimSpace(strings.TrimRight(strings.TrimRight(strings.TrimSpace(formatFloat(t)), "0"), "."))
	default:
		return ""
	}
}

func normalizeArtifactPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	path = strings.Trim(path, `"`)
	path = strings.TrimPrefix(path, "./")
	path = strings.ReplaceAll(path, "\\", "/")
	return path
}

func buildSimplePatch(oldStr, newStr string) string {
	oldStr = strings.TrimSuffix(oldStr, "\n")
	newStr = strings.TrimSuffix(newStr, "\n")
	var b strings.Builder
	b.WriteString("--- old\n+++ new\n@@\n")
	if oldStr != "" {
		for _, l := range strings.Split(oldStr, "\n") {
			b.WriteString("-")
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	if newStr != "" {
		for _, l := range strings.Split(newStr, "\n") {
			b.WriteString("+")
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func truncateLarge(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max]
}

func formatFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
