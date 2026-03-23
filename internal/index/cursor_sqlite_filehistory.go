package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

type sqliteFileHistorySession struct {
	ConversationID string
	ProjectDir     string
	ProjectName    string
	UpdatedAt      int64
	SourcePath     string
	Files          []model.FileVersionInfo
}

func indexCursorSQLiteFileHistory(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, opts RunOptions) (int, error) {
	return indexCursorSQLiteFileHistoryWithFilter(ctx, cursorDir, s, tx, nil, opts)
}

func indexCursorSQLiteFileHistoryFiltered(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, opts RunOptions) (int, error) {
	return indexCursorSQLiteFileHistoryWithFilter(ctx, cursorDir, s, tx, changed, opts)
}

func indexCursorSQLiteFileHistoryWithFilter(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, opts RunOptions) (int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, nil
	}
	appDir := findCursorAppDir(cursorDir)
	if appDir == "" {
		return 0, nil
	}

	globalDB := filepath.Join(appDir, "User", "globalStorage", "state.vscdb")
	wsDir := filepath.Join(appDir, "User", "workspaceStorage")

	var sessions []sqliteFileHistorySession
	if _, err := os.Stat(globalDB); err == nil && (changed == nil || changed[globalDB]) && cursorSQLiteDBAllowed(globalDB, opts) {
		if g, err := extractGlobalComposerFileHistory(globalDB); err == nil {
			sessions = append(sessions, g...)
		}
	}
	if _, err := os.Stat(wsDir); err == nil {
		entries, _ := os.ReadDir(wsDir)
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "ext-dev" {
				continue
			}
			dbPath := filepath.Join(wsDir, e.Name(), "state.vscdb")
			if _, err := os.Stat(dbPath); err != nil || (changed != nil && !changed[dbPath]) || !cursorSQLiteDBAllowed(dbPath, opts) {
				continue
			}
			workspaceID := e.Name()
			if ws, err := extractWorkspaceComposerFileHistory(dbPath); err == nil {
				sessions = append(sessions, ws...)
			}
			if chat, err := extractChatModeFileHistory(dbPath, workspaceID); err == nil {
				sessions = append(sessions, chat...)
			}
			if ai, err := extractAiServiceFileHistory(dbPath, workspaceID); err == nil {
				sessions = append(sessions, ai...)
			}
		}
	}

	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt != sessions[j].UpdatedAt {
			return sessions[i].UpdatedAt > sessions[j].UpdatedAt
		}
		return sessions[i].ConversationID < sessions[j].ConversationID
	})

	count := 0
	for _, sess := range sessions {
		sessionDBID := int64(0)
		if dbID, err := s.GetSessionDBIDForProject(ctx, tx, sess.ProjectDir, sess.ConversationID); err == nil {
			sessionDBID = dbID
			_ = s.LinkFileHistoryToSession(ctx, tx, sess.ConversationID, dbID)
		}
		if err := s.InsertFileHistoryWithMeta(ctx, tx, sess.ConversationID, sess.Files, sessionDBID, sess.UpdatedAt, model.SourceCursor, sess.SourcePath); err != nil {
			continue
		}
		count++
	}
	return count, nil
}

func extractGlobalComposerFileHistory(dbPath string) ([]sqliteFileHistorySession, error) {
	db, err := openSQLiteRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	changesByComposer := make(map[string][]cursorFileChange)

	rows, err := db.Query("SELECT key, value FROM cursorDiskKV WHERE key LIKE 'composerData:%' ORDER BY key")
	if err == nil {
		for rows.Next() {
			var key string
			var value sql.NullString
			if rows.Scan(&key, &value) != nil || !value.Valid {
				continue
			}
			var payload map[string]any
			if json.Unmarshal([]byte(value.String), &payload) != nil {
				continue
			}
			composerID := firstStringFromMap(payload, "composerId")
			if composerID == "" {
				parts := strings.SplitN(key, ":", 2)
				if len(parts) == 2 {
					composerID = parts[1]
				}
			}
			if composerID == "" {
				continue
			}
			ts := timestampStringFromAny(payload["lastUpdatedAt"])
			for _, b := range asSlice(payload["conversation"]) {
				changesByComposer[composerID] = append(changesByComposer[composerID], extractSQLiteBubbleChanges(asMap(b), ts)...)
			}
		}
		rows.Close()
	}

	bRows, err := db.Query("SELECT key, value FROM cursorDiskKV WHERE key LIKE 'bubbleId:%' ORDER BY key")
	if err == nil {
		for bRows.Next() {
			var key string
			var value sql.NullString
			if bRows.Scan(&key, &value) != nil || !value.Valid {
				continue
			}
			parts := strings.SplitN(key, ":", 3)
			if len(parts) < 3 {
				continue
			}
			composerID := parts[1]
			var bubble map[string]any
			if json.Unmarshal([]byte(value.String), &bubble) != nil {
				continue
			}
			ts := timestampStringFromAny(bubble["timestamp"])
			if ts == "" {
				ts = timestampStringFromAny(bubble["createdAt"])
			}
			changesByComposer[composerID] = append(changesByComposer[composerID], extractSQLiteBubbleChanges(bubble, ts)...)
		}
		bRows.Close()
	}

	sourceKey := "cursor-global-composer"
	projectDir := sqliteProjectDirForSource(sourceKey)
	projectName := sqliteProjectLabelForSource(sourceKey)

	var out []sqliteFileHistorySession
	for composerID, changes := range changesByComposer {
		files := buildFileVersionsFromChanges(changes)
		if len(files) == 0 {
			continue
		}
		out = append(out, sqliteFileHistorySession{
			ConversationID: composerID,
			ProjectDir:     projectDir,
			ProjectName:    projectName,
			UpdatedAt:      latestFileVersionTimestampMs(files),
			SourcePath:     dbPath,
			Files:          files,
		})
	}
	return out, nil
}

func extractWorkspaceComposerFileHistory(dbPath string) ([]sqliteFileHistorySession, error) {
	db, err := openSQLiteRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var wsJSON sql.NullString
	if db.QueryRow("SELECT value FROM ItemTable WHERE key = 'composer.composerData'").Scan(&wsJSON) != nil || !wsJSON.Valid {
		return nil, nil
	}

	var payload map[string]any
	if json.Unmarshal([]byte(wsJSON.String), &payload) != nil {
		return nil, nil
	}

	sourceKey := "cursor-workspace-composer"
	projectDir := sqliteProjectDirForSource(sourceKey)
	projectName := sqliteProjectLabelForSource(sourceKey)

	var out []sqliteFileHistorySession
	for _, comp := range asSlice(payload["allComposers"]) {
		compMap := asMap(comp)
		conversationID := firstStringFromMap(compMap, "composerId")
		if conversationID == "" {
			continue
		}
		var changes []cursorFileChange
		for _, b := range asSlice(compMap["conversation"]) {
			bubble := asMap(b)
			ts := timestampStringFromAny(bubble["timestamp"])
			changes = append(changes, extractSQLiteBubbleChanges(bubble, ts)...)
		}
		files := buildFileVersionsFromChanges(changes)
		if len(files) == 0 {
			continue
		}
		out = append(out, sqliteFileHistorySession{
			ConversationID: conversationID,
			ProjectDir:     projectDir,
			ProjectName:    projectName,
			UpdatedAt:      latestFileVersionTimestampMs(files),
			SourcePath:     dbPath,
			Files:          files,
		})
	}
	return out, nil
}

func extractChatModeFileHistory(dbPath, workspaceID string) ([]sqliteFileHistorySession, error) {
	db, err := openSQLiteRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var chatJSON sql.NullString
	if db.QueryRow("SELECT value FROM ItemTable WHERE key = 'workbench.panel.aichat.view.aichat.chatdata'").Scan(&chatJSON) != nil || !chatJSON.Valid {
		return nil, nil
	}
	var payload map[string]any
	if json.Unmarshal([]byte(chatJSON.String), &payload) != nil {
		return nil, nil
	}

	sourceKey := "cursor-chat"
	projectDir := sqliteProjectDirForSource(sourceKey)
	projectName := sqliteProjectLabelForSource(sourceKey)

	fallbackIdx := 0
	var out []sqliteFileHistorySession
	for _, tabAny := range asSlice(payload["tabs"]) {
		tab := asMap(tabAny)
		bubbles := asSlice(tab["bubbles"])
		if len(bubbles) == 0 {
			continue
		}

		msgCount := 0
		for _, b := range bubbles {
			if firstStringFromMap(asMap(b), "rawText", "text") != "" {
				msgCount++
			}
		}
		if msgCount == 0 {
			continue
		}
		conversationID := firstStringFromMap(tab, "tabId")
		if conversationID == "" {
			conversationID = fmt.Sprintf("chat-%s-%d", shortWorkspaceID(workspaceID), fallbackIdx)
		}
		fallbackIdx++

		var changes []cursorFileChange
		for _, b := range bubbles {
			bubble := asMap(b)
			ts := timestampStringFromAny(bubble["timestamp"])
			changes = append(changes, extractSQLiteBubbleChanges(bubble, ts)...)
		}
		files := buildFileVersionsFromChanges(changes)
		if len(files) == 0 {
			continue
		}
		out = append(out, sqliteFileHistorySession{
			ConversationID: conversationID,
			ProjectDir:     projectDir,
			ProjectName:    projectName,
			UpdatedAt:      latestFileVersionTimestampMs(files),
			SourcePath:     dbPath,
			Files:          files,
		})
	}
	return out, nil
}

func extractAiServiceFileHistory(dbPath, workspaceID string) ([]sqliteFileHistorySession, error) {
	db, err := openSQLiteRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var promptsJSON, gensJSON sql.NullString
	_ = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'aiService.prompts'").Scan(&promptsJSON)
	_ = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'aiService.generations'").Scan(&gensJSON)
	if !promptsJSON.Valid {
		return nil, nil
	}

	var prompts []map[string]any
	if json.Unmarshal([]byte(promptsJSON.String), &prompts) != nil {
		return nil, nil
	}
	var generations []map[string]any
	if gensJSON.Valid {
		_ = json.Unmarshal([]byte(gensJSON.String), &generations)
	}

	sourceKey := "cursor-aiservice"
	projectDir := sqliteProjectDirForSource(sourceKey)
	projectName := sqliteProjectLabelForSource(sourceKey)

	maxLen := len(prompts)
	if len(generations) > maxLen {
		maxLen = len(generations)
	}

	var out []sqliteFileHistorySession
	for i := 0; i < maxLen; i++ {
		msgCount := 0
		if i < len(prompts) && firstStringFromMap(prompts[i], "text") != "" {
			msgCount++
		}
		if i < len(generations) && firstStringFromMap(generations[i], "text", "message") != "" {
			msgCount++
		}
		if msgCount == 0 {
			continue
		}

		conversationID := fmt.Sprintf("aiservice-%s-%d", shortWorkspaceID(workspaceID), i)
		var changes []cursorFileChange
		if i < len(prompts) {
			changes = append(changes, extractSQLiteBubbleChanges(prompts[i], "")...)
		}
		if i < len(generations) {
			changes = append(changes, extractSQLiteBubbleChanges(generations[i], "")...)
		}

		files := buildFileVersionsFromChanges(changes)
		if len(files) == 0 {
			continue
		}
		out = append(out, sqliteFileHistorySession{
			ConversationID: conversationID,
			ProjectDir:     projectDir,
			ProjectName:    projectName,
			UpdatedAt:      latestFileVersionTimestampMs(files),
			SourcePath:     dbPath,
			Files:          files,
		})
	}
	return out, nil
}

func extractSQLiteBubbleChanges(payload map[string]any, ts string) []cursorFileChange {
	if len(payload) == 0 {
		return nil
	}
	if ts == "" {
		ts = timestampStringFromAny(payload["timestamp"])
	}

	var changes []cursorFileChange
	changes = append(changes, extractSQLiteChangesFromCollection(payload["codeBlocks"], ts)...)
	changes = append(changes, extractSQLiteChangesFromCollection(payload["suggestedCodeBlocks"], ts)...)
	changes = append(changes, extractSQLiteChangesFromCollection(payload["diffHistories"], ts)...)
	changes = append(changes, extractSQLiteChangesFromCollection(payload["suggestedDiffs"], ts)...)
	changes = append(changes, extractSQLiteSelectionChanges(payload["selections"], ts)...)
	if ctx := asMap(payload["context"]); len(ctx) > 0 {
		changes = append(changes, extractSQLiteSelectionChanges(ctx["selections"], ts)...)
	}
	if c := sqliteChangeFromMap(payload, ts); c != nil {
		changes = append(changes, *c)
	}
	return changes
}

func extractSQLiteChangesFromCollection(v any, ts string) []cursorFileChange {
	items := asSlice(v)
	if len(items) == 0 {
		return nil
	}
	var out []cursorFileChange
	for _, item := range items {
		m := asMap(item)
		if len(m) == 0 {
			continue
		}
		if c := sqliteChangeFromMap(m, ts); c != nil {
			out = append(out, *c)
		}
		out = append(out, extractSQLiteChangesFromCollection(m["codeBlocks"], ts)...)
		out = append(out, extractSQLiteChangesFromCollection(m["suggestedCodeBlocks"], ts)...)
		out = append(out, extractSQLiteChangesFromCollection(m["diffHistories"], ts)...)
		out = append(out, extractSQLiteChangesFromCollection(m["suggestedDiffs"], ts)...)
		out = append(out, extractSQLiteSelectionChanges(m["selections"], ts)...)
		if ctx := asMap(m["context"]); len(ctx) > 0 {
			out = append(out, extractSQLiteSelectionChanges(ctx["selections"], ts)...)
		}
	}
	return out
}

func extractSQLiteSelectionChanges(v any, ts string) []cursorFileChange {
	items := asSlice(v)
	if len(items) == 0 {
		return nil
	}
	var out []cursorFileChange
	for _, item := range items {
		m := asMap(item)
		if len(m) == 0 {
			continue
		}
		path := extractFilePathFromMap(m)
		if path == "" {
			continue
		}
		text := truncateLarge(firstStringFromMap(m, "text", "rawText"), 40_000)
		out = append(out, cursorFileChange{
			FilePath:   path,
			Patch:      text,
			ChangeKind: "metadata",
			Timestamp:  ts,
		})
	}
	return out
}

func sqliteChangeFromMap(m map[string]any, ts string) *cursorFileChange {
	path := extractFilePathFromMap(m)
	if path == "" {
		return nil
	}
	diff := truncateLarge(firstStringFromMap(m, "diff", "patch", "unifiedDiff"), 120_000)
	content := truncateLarge(firstStringFromMap(m, "content", "newText", "new_string", "newString", "text", "code"), 200_000)

	change := cursorFileChange{
		FilePath:  path,
		Timestamp: ts,
	}
	switch {
	case diff != "":
		change.Patch = diff
		change.ChangeKind = "patch"
	case content != "":
		change.Content = content
		change.ChangeKind = "content"
	default:
		change.ChangeKind = "metadata"
	}
	return &change
}

func extractFilePathFromMap(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	for _, key := range []string{"file_path", "filePath", "path", "file", "filepath", "relativeWorkspacePath"} {
		if p := normalizeArtifactPath(anyString(m[key])); p != "" {
			return p
		}
	}
	if uri := m["uri"]; uri != nil {
		switch t := uri.(type) {
		case string:
			if p := normalizeArtifactPath(t); p != "" {
				return p
			}
		case map[string]any:
			for _, key := range []string{"fsPath", "path", "uri"} {
				if p := normalizeArtifactPath(anyString(t[key])); p != "" {
					return p
				}
			}
		}
	}
	return ""
}

func timestampStringFromAny(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return normalizeNumericTimestamp(int64(t))
	case int64:
		return normalizeNumericTimestamp(t)
	case int:
		return normalizeNumericTimestamp(int64(t))
	case json.Number:
		n, _ := t.Int64()
		return normalizeNumericTimestamp(n)
	default:
		return ""
	}
}

func normalizeNumericTimestamp(v int64) string {
	if v <= 0 {
		return ""
	}
	if v < 1_000_000_000_000 {
		v *= 1000
	}
	return time.UnixMilli(v).UTC().Format(time.RFC3339)
}

func sqliteProjectDirForSource(source string) string {
	return "cursor-sqlite-" + strings.ReplaceAll(source, "cursor-", "")
}

func sqliteProjectLabelForSource(source string) string {
	switch source {
	case "cursor-global-composer":
		return "Cursor Composer"
	case "cursor-workspace-composer":
		return "Cursor Workspace Composer (Legacy)"
	case "cursor-aiservice":
		return "Cursor AI Service (Legacy)"
	case "cursor-chat":
		return "Cursor Chat (Legacy)"
	default:
		return source
	}
}

func shortWorkspaceID(workspaceID string) string {
	if len(workspaceID) <= 8 {
		return workspaceID
	}
	return workspaceID[:8]
}

func latestFileVersionTimestampMs(files []model.FileVersionInfo) int64 {
	var latest int64
	for _, f := range files {
		ts := parseTimeStringToUnixMilli(f.Timestamp)
		if ts > latest {
			latest = ts
		}
	}
	return latest
}
