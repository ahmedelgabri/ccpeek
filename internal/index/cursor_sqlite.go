package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

type cachedSession struct {
	SessionID    string
	Name         string
	Source       string
	FirstPrompt  string
	MessageCount int
	CreatedAt    int64
	UpdatedAt    int64
	ModelName    string
	SourcePath   string
}

func defaultCursorAppDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor")
	case "linux":
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "Cursor")
		}
		return filepath.Join(home, ".config", "Cursor")
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "Cursor")
		}
		return filepath.Join(home, "AppData", "Roaming", "Cursor")
	default:
		return filepath.Join(home, ".config", "Cursor")
	}
}

func hasCursorSQLiteLayout(appDir string) bool {
	if appDir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(appDir, "User", "globalStorage", "state.vscdb")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(appDir, "User", "workspaceStorage")); err == nil {
		return true
	}
	return false
}

func findCursorAppDir(cursorDir string) string {
	candidates := make([]string, 0, 3)
	if cursorDir != "" {
		candidates = append(candidates, cursorDir)
		candidates = append(candidates, filepath.Join(cursorDir, "Cursor"))
		if shouldFallbackToDefaultCursorAppDir(cursorDir) {
			candidates = append(candidates, defaultCursorAppDir())
		}
	} else {
		candidates = append(candidates, defaultCursorAppDir())
	}

	for _, c := range candidates {
		if hasCursorSQLiteLayout(c) {
			return c
		}
	}
	return ""
}

func shouldFallbackToDefaultCursorAppDir(cursorDir string) bool {
	home, _ := os.UserHomeDir()
	if home == "" {
		return false
	}
	return filepath.Clean(cursorDir) == filepath.Clean(filepath.Join(home, ".cursor"))
}

func openSQLiteRO(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?mode=ro&_busy_timeout=5000", path)
	return sql.Open("sqlite3", dsn)
}

func indexCursorSQLite(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, opts RunOptions) (int, int, error) {
	return indexCursorSQLiteWithFilter(ctx, cursorDir, s, tx, nil, opts)
}

func indexCursorSQLiteFiltered(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, opts RunOptions) (int, int, error) {
	return indexCursorSQLiteWithFilter(ctx, cursorDir, s, tx, changed, opts)
}

func indexCursorSQLiteWithFilter(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool, opts RunOptions) (int, int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, 0, nil
	}
	appDir := findCursorAppDir(cursorDir)
	if appDir == "" {
		return 0, 0, nil
	}

	globalDB := filepath.Join(appDir, "User", "globalStorage", "state.vscdb")
	wsDir := filepath.Join(appDir, "User", "workspaceStorage")

	var all []cachedSession
	if _, err := os.Stat(globalDB); err == nil && (changed == nil || changed[globalDB]) && cursorSQLiteDBAllowed(globalDB, opts) {
		sessions, err := extractGlobalComposers(globalDB)
		if err == nil {
			all = append(all, sessions...)
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
			if ai, err := extractAiService(dbPath, workspaceID); err == nil {
				all = append(all, ai...)
			}
			if chat, err := extractChatMode(dbPath, workspaceID); err == nil {
				all = append(all, chat...)
			}
			if wsComp, err := extractWorkspaceComposers(dbPath, workspaceID); err == nil {
				all = append(all, wsComp...)
			}
		}
	}

	if len(all) == 0 {
		return 0, 0, nil
	}

	bySource := make(map[string][]cachedSession)
	for _, sess := range all {
		bySource[sess.Source] = append(bySource[sess.Source], sess)
	}
	sourceLabels := map[string]string{
		"cursor-global-composer":    "Cursor Composer",
		"cursor-workspace-composer": "Cursor Workspace Composer (Legacy)",
		"cursor-aiservice":          "Cursor AI Service (Legacy)",
		"cursor-chat":               "Cursor Chat (Legacy)",
	}

	projectCount := 0
	totalSessions := 0
	for source, sessions := range bySource {
		label := sourceLabels[source]
		if label == "" {
			label = source
		}
		dirName := "cursor-sqlite-" + strings.ReplaceAll(source, "cursor-", "")

		updatedAt := int64(0)
		for _, sess := range sessions {
			ts := sess.UpdatedAt
			if ts == 0 {
				ts = sess.CreatedAt
			}
			if ts > updatedAt {
				updatedAt = ts
			}
		}
		projectID, err := s.UpsertProjectWithMeta(ctx, tx, dirName, label, "", model.SourceCursor, updatedAt, true)
		if err != nil {
			continue
		}
		projectCount++

		for _, sess := range sessions {
			created := ""
			modified := ""
			if sess.CreatedAt > 0 {
				created = time.UnixMilli(sess.CreatedAt).UTC().Format(time.RFC3339)
			}
			if sess.UpdatedAt > 0 {
				modified = time.UnixMilli(sess.UpdatedAt).UTC().Format(time.RFC3339)
			} else {
				modified = created
			}
			entry := model.SessionEntry{
				SessionID:    sess.SessionID,
				FirstPrompt:  sess.FirstPrompt,
				MessageCount: sess.MessageCount,
				Created:      created,
				Modified:     modified,
				MetadataOnly: true,
				ModelName:    sess.ModelName,
				Source:       model.SourceCursor,
			}
			if _, err := s.InsertSession(ctx, tx, projectID, entry, sess.SourcePath); err != nil {
				continue
			}

			display := sess.FirstPrompt
			if display == "" {
				display = sess.Name
			}
			if display == "" {
				display = sess.SessionID
			}
			timestamp := sess.UpdatedAt
			if timestamp == 0 {
				timestamp = sess.CreatedAt
			}
			_ = s.InsertHistory(ctx, tx, model.HistoryEntry{
				Display:    display,
				Timestamp:  timestamp,
				Project:    label,
				ProjectDir: dirName,
				Source:     model.SourceCursor,
			}, sess.SourcePath)
			totalSessions++
		}
	}

	return projectCount, totalSessions, nil
}

func cursorSQLiteDBAllowed(dbPath string, opts RunOptions) bool {
	if !opts.IncludeCursorSQLite {
		return false
	}
	if opts.MaxCursorSQLiteBytes <= 0 {
		return true
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return false
	}
	return info.Size() <= opts.MaxCursorSQLiteBytes
}

// extractGlobalComposers reads global composer metadata and bubble counts.
func extractGlobalComposers(dbPath string) ([]cachedSession, error) {
	db, err := openSQLiteRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	type composerMeta struct {
		ComposerID    string
		Name          string
		CreatedAt     int64
		LastUpdatedAt int64
		ModelName     string
	}
	composers := make(map[string]composerMeta)

	rows, err := db.Query("SELECT key, value FROM cursorDiskKV WHERE key LIKE 'composerData:%'")
	if err != nil {
		return nil, err
	}
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
		composerID := anyString(payload["composerId"])
		if composerID == "" {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 {
				composerID = parts[1]
			}
		}
		if composerID == "" {
			continue
		}
		modelName := ""
		if cfg := asMap(payload["modelConfig"]); len(cfg) > 0 {
			modelName = anyString(cfg["modelName"])
		}
		composers[composerID] = composerMeta{
			ComposerID:    composerID,
			Name:          anyString(payload["name"]),
			CreatedAt:     timestampFromAny(payload["createdAt"]),
			LastUpdatedAt: timestampFromAny(payload["lastUpdatedAt"]),
			ModelName:     modelName,
		}
	}
	rows.Close()

	bRows, err := db.Query("SELECT key FROM cursorDiskKV WHERE key LIKE 'bubbleId:%'")
	if err != nil {
		return nil, err
	}
	bubbleCounts := make(map[string]int)
	for bRows.Next() {
		var key string
		if bRows.Scan(&key) != nil {
			continue
		}
		parts := strings.SplitN(key, ":", 3)
		if len(parts) >= 3 {
			bubbleCounts[parts[1]]++
		}
	}
	bRows.Close()

	var sessions []cachedSession
	for cid, cm := range composers {
		msgCount := bubbleCounts[cid]
		if msgCount == 0 {
			continue
		}
		firstPrompt := cm.Name
		if firstPrompt == "" || firstPrompt == "Untitled" {
			if len(cid) >= 8 {
				firstPrompt = cid[:8] + "..."
			} else {
				firstPrompt = cid
			}
		}
		firstPrompt = stripCursorSystemTags(firstPrompt)
		if len(firstPrompt) > 200 {
			firstPrompt = firstPrompt[:200]
		}
		sessions = append(sessions, cachedSession{
			SessionID:    cid,
			Name:         cm.Name,
			Source:       "cursor-global-composer",
			FirstPrompt:  firstPrompt,
			MessageCount: msgCount,
			CreatedAt:    cm.CreatedAt,
			UpdatedAt:    cm.LastUpdatedAt,
			ModelName:    cm.ModelName,
			SourcePath:   dbPath,
		})
	}
	return sessions, nil
}

func extractAiService(dbPath, workspaceID string) ([]cachedSession, error) {
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

	maxLen := len(prompts)
	if len(generations) > maxLen {
		maxLen = len(generations)
	}
	wsShort := workspaceID
	if len(wsShort) > 8 {
		wsShort = wsShort[:8]
	}

	var sessions []cachedSession
	for i := 0; i < maxLen; i++ {
		msgCount := 0
		firstPrompt := ""
		if i < len(prompts) {
			text := anyString(prompts[i]["text"])
			if text != "" {
				msgCount++
				if len(text) > 200 {
					text = text[:200]
				}
				firstPrompt = text
			}
		}
		if i < len(generations) {
			text := anyString(generations[i]["text"])
			if text == "" {
				text = anyString(generations[i]["message"])
			}
			if text != "" {
				msgCount++
			}
		}
		if msgCount == 0 {
			continue
		}
		sessions = append(sessions, cachedSession{
			SessionID:    fmt.Sprintf("aiservice-%s-%d", wsShort, i),
			Name:         firstPrompt,
			Source:       "cursor-aiservice",
			FirstPrompt:  firstPrompt,
			MessageCount: msgCount,
			SourcePath:   dbPath,
		})
	}
	return sessions, nil
}

func extractChatMode(dbPath, workspaceID string) ([]cachedSession, error) {
	db, err := openSQLiteRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var chatJSON sql.NullString
	_ = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'workbench.panel.aichat.view.aichat.chatdata'").Scan(&chatJSON)
	if !chatJSON.Valid {
		return nil, nil
	}

	type chatBubble struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		RawText string `json:"rawText"`
	}
	type chatTab struct {
		TabID     string       `json:"tabId"`
		ChatTitle string       `json:"chatTitle"`
		Bubbles   []chatBubble `json:"bubbles"`
	}
	type chatData struct {
		Tabs []chatTab `json:"tabs"`
	}
	var cd chatData
	if json.Unmarshal([]byte(chatJSON.String), &cd) != nil {
		return nil, nil
	}

	var sessions []cachedSession
	for _, tab := range cd.Tabs {
		msgCount := 0
		firstPrompt := ""
		for _, b := range tab.Bubbles {
			text := b.RawText
			if text == "" {
				text = b.Text
			}
			if text == "" {
				continue
			}
			msgCount++
			if firstPrompt == "" && b.Type == "user" {
				if len(text) > 200 {
					text = text[:200]
				}
				firstPrompt = text
			}
		}
		if msgCount == 0 {
			continue
		}
		sessionID := tab.TabID
		if sessionID == "" {
			wsShort := workspaceID
			if len(wsShort) > 8 {
				wsShort = wsShort[:8]
			}
			sessionID = fmt.Sprintf("chat-%s-%d", wsShort, len(sessions))
		}
		name := tab.ChatTitle
		if name == "" {
			name = firstPrompt
		}
		sessions = append(sessions, cachedSession{
			SessionID:    sessionID,
			Name:         name,
			Source:       "cursor-chat",
			FirstPrompt:  firstPrompt,
			MessageCount: msgCount,
			SourcePath:   dbPath,
		})
	}
	return sessions, nil
}

func extractWorkspaceComposers(dbPath, workspaceID string) ([]cachedSession, error) {
	db, err := openSQLiteRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	var wsJSON sql.NullString
	_ = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'composer.composerData'").Scan(&wsJSON)
	if !wsJSON.Valid {
		return nil, nil
	}

	type bubble struct {
		Type int    `json:"type"`
		Text string `json:"text"`
	}
	type wsComposer struct {
		ComposerID   string                 `json:"composerId"`
		Name         string                 `json:"name"`
		Conversation []bubble               `json:"conversation"`
		ModelConfig  map[string]interface{} `json:"modelConfig"`
	}
	type wsData struct {
		AllComposers []wsComposer `json:"allComposers"`
	}
	var wcd wsData
	if json.Unmarshal([]byte(wsJSON.String), &wcd) != nil {
		return nil, nil
	}

	var sessions []cachedSession
	for _, comp := range wcd.AllComposers {
		msgCount := 0
		firstPrompt := ""
		for _, b := range comp.Conversation {
			if b.Text == "" {
				continue
			}
			msgCount++
			if firstPrompt == "" && b.Type == 1 {
				fp := b.Text
				if len(fp) > 200 {
					fp = fp[:200]
				}
				firstPrompt = fp
			}
		}
		if msgCount == 0 {
			continue
		}
		name := comp.Name
		if name == "" {
			name = firstPrompt
		}
		modelName := ""
		if comp.ModelConfig != nil {
			if v, ok := comp.ModelConfig["modelName"].(string); ok {
				modelName = v
			}
		}
		sessions = append(sessions, cachedSession{
			SessionID:    comp.ComposerID,
			Name:         name,
			Source:       "cursor-workspace-composer",
			FirstPrompt:  firstPrompt,
			MessageCount: msgCount,
			ModelName:    modelName,
			SourcePath:   dbPath,
		})
	}
	return sessions, nil
}

func timestampFromAny(v any) int64 {
	switch t := v.(type) {
	case float64:
		if t > 1_000_000_000_000 {
			return int64(t)
		}
		if t > 1_000_000_000 {
			return int64(t * 1000)
		}
	case int64:
		if t > 1_000_000_000_000 {
			return t
		}
		if t > 1_000_000_000 {
			return t * 1000
		}
	case json.Number:
		if n, err := t.Int64(); err == nil {
			if n > 1_000_000_000_000 {
				return n
			}
			if n > 1_000_000_000 {
				return n * 1000
			}
		}
	case string:
		return parseTimeStringToUnixMilli(t)
	}
	return 0
}
