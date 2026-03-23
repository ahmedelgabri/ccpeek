package index

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexCursorProjects(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx) (int, int, error) {
	return indexCursorProjectsWithFilter(ctx, cursorDir, s, tx, nil)
}

func indexCursorProjectsFiltered(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, int, error) {
	return indexCursorProjectsWithFilter(ctx, cursorDir, s, tx, changed)
}

func indexCursorProjectsWithFilter(ctx context.Context, cursorDir string, s *store.Store, tx *sqlx.Tx, changed map[string]bool) (int, int, error) {
	if strings.TrimSpace(cursorDir) == "" {
		return 0, 0, nil
	}
	projectsDir := filepath.Join(cursorDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	projectCount := 0
	totalSessions := 0

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

		type sessionWithMessages struct {
			entry    model.SessionEntry
			messages []model.ConversationMessage
			srcPath  string
		}
		var sessions []sessionWithMessages
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
			entry := buildCursorSessionEntry(sessionID, jsonlPath, messages)
			sessions = append(sessions, sessionWithMessages{
				entry:    entry,
				messages: messages,
				srcPath:  jsonlPath,
			})
		}
		if len(sessions) == 0 {
			continue
		}

		sort.Slice(sessions, func(i, j int) bool {
			return sessionUpdatedAtMs(sessions[i].entry) > sessionUpdatedAtMs(sessions[j].entry)
		})

		projectDisplay := decodeCursorProjectDir(dirName)
		projectUpdatedAt := sessionUpdatedAtMs(sessions[0].entry)
		projectID, err := s.UpsertProjectWithMeta(ctx, tx, dirName, projectDisplay, projectDisplay, model.SourceCursor, projectUpdatedAt, true)
		if err != nil {
			continue
		}
		projectCount++

		for _, sess := range sessions {
			sessionDBID, err := s.InsertSession(ctx, tx, projectID, sess.entry, sess.srcPath)
			if err != nil {
				continue
			}
			if err := s.InsertMessages(ctx, tx, sessionDBID, sess.messages); err != nil {
				continue
			}
			if err := s.InsertCommands(ctx, tx, sessionDBID, sess.messages); err != nil {
				continue
			}

			display := sess.entry.FirstPrompt
			if display == "" {
				display = sess.entry.SessionID
			}
			_ = s.InsertHistory(ctx, tx, model.HistoryEntry{
				Display:    display,
				Timestamp:  sessionUpdatedAtMs(sess.entry),
				Project:    projectDisplay,
				ProjectDir: dirName,
				Source:     model.SourceCursor,
			}, sess.srcPath)

			totalSessions++
		}
	}

	return projectCount, totalSessions, nil
}

func buildCursorSessionEntry(sessionID, jsonlPath string, messages []model.ConversationMessage) model.SessionEntry {
	toolCounts := countToolUses(messages)
	bashCount := toolCounts["Bash"] + toolCounts["Shell"]
	tokens := estimateTokens(messages)

	var firstPrompt string
	for _, m := range messages {
		if m.Message.Role == "user" {
			text := stripCursorSystemTags(m.Message.ContentText())
			if len(text) > 200 {
				text = text[:200]
			}
			firstPrompt = text
			break
		}
	}

	info, _ := os.Stat(jsonlPath)
	var created, modified string
	if info != nil {
		modified = info.ModTime().UTC().Format(time.RFC3339)
		created = modified
	}

	return model.SessionEntry{
		SessionID:        sessionID,
		FirstPrompt:      firstPrompt,
		MessageCount:     len(messages),
		Created:          created,
		Modified:         modified,
		BashCommandCount: bashCount,
		ToolUseCounts:    toolCounts,
		EstimatedTokens:  tokens,
		Source:           model.SourceCursor,
	}
}

func decodeCursorProjectDir(dirName string) string {
	return model.DecodeProjectDir(dirName)
}

func stripCursorSystemTags(text string) string {
	if idx := strings.Index(text, "<user_query>"); idx >= 0 {
		text = text[idx+len("<user_query>"):]
		if end := strings.Index(text, "</user_query>"); end >= 0 {
			text = text[:end]
		}
	}
	return strings.TrimSpace(text)
}

func cursorSessionPath(transcriptsDir string, item os.DirEntry) (sessionID string, jsonlPath string, ok bool) {
	if item.IsDir() {
		sessionID = item.Name()
		jsonlPath = filepath.Join(transcriptsDir, sessionID, sessionID+".jsonl")
		return sessionID, jsonlPath, true
	}
	if strings.HasSuffix(item.Name(), ".jsonl") {
		sessionID = strings.TrimSuffix(item.Name(), ".jsonl")
		jsonlPath = filepath.Join(transcriptsDir, item.Name())
		return sessionID, jsonlPath, true
	}
	return "", "", false
}
