package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func indexProjects(claudeDir string, s *store.Store, tx *sqlx.Tx) (int, int, error) {
	srcDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(srcDir)
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
		projectDir := filepath.Join(srcDir, dirName)
		displayName := decodeProjectDir(dirName)

		// Read sessions-index.json if available
		var sessionsIndex model.SessionsIndex
		indexPath := filepath.Join(projectDir, "sessions-index.json")
		if data, err := os.ReadFile(indexPath); err == nil {
			_ = json.Unmarshal(data, &sessionsIndex)
		}

		// Find JSONL session files
		files, err := os.ReadDir(projectDir)
		if err != nil {
			continue
		}

		// Build session entries first to sort them
		type sessionWithMessages struct {
			entry    model.SessionEntry
			messages []model.ConversationMessage
		}
		var sessionsData []sessionWithMessages

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}

			sessionID := strings.TrimSuffix(f.Name(), ".jsonl")
			jsonlPath := filepath.Join(projectDir, f.Name())

			lines, err := readJSONL[model.RawJSONLLine](jsonlPath)
			if err != nil {
				continue
			}

			// Filter to conversation messages
			var messages []model.ConversationMessage
			for _, line := range lines {
				if line.Type != "user" && line.Type != "assistant" && line.Type != "system" {
					continue
				}
				msg := model.ConversationMessage{
					Type:      line.Type,
					Timestamp: line.Timestamp,
					UUID:      line.UUID,
					SessionID: line.SessionID,
					Cwd:       line.Cwd,
					GitBranch: line.GitBranch,
				}
				if line.Message != nil {
					msg.Message = *line.Message
				} else {
					msg.Message = model.MessagePayload{
						Role:    line.Type,
						Content: json.RawMessage(`""`),
					}
				}
				messages = append(messages, msg)
			}

			// Build session metadata
			session := buildSessionEntry(sessionID, messages, sessionsIndex.Entries)
			sessionsData = append(sessionsData, sessionWithMessages{entry: session, messages: messages})
		}

		if len(sessionsData) == 0 {
			continue
		}

		// Sort sessions by modified date (newest first) before inserting
		sort.Slice(sessionsData, func(i, j int) bool {
			ti, _ := time.Parse(time.RFC3339Nano, sessionsData[i].entry.Modified)
			tj, _ := time.Parse(time.RFC3339Nano, sessionsData[j].entry.Modified)
			return ti.After(tj)
		})

		// Insert project
		projectID, err := s.InsertProject(tx, dirName, displayName)
		if err != nil {
			continue
		}
		projectCount++

		// Insert sessions and their messages
		for _, sd := range sessionsData {
			sessionDBID, err := s.InsertSession(tx, projectID, sd.entry)
			if err != nil {
				continue
			}

			if err := s.InsertMessages(tx, sessionDBID, sd.messages); err != nil {
				continue
			}
			totalSessions++
		}
	}

	return projectCount, totalSessions, nil
}

func buildSessionEntry(sessionID string, messages []model.ConversationMessage, indexEntries []model.SessionEntry) model.SessionEntry {
	toolCounts := countToolUses(messages)
	bashCount := toolCounts["Bash"]
	tokens := estimateTokens(messages)

	// Check sessions-index for metadata
	for _, ie := range indexEntries {
		if ie.SessionID == sessionID {
			entry := model.SessionEntry{
				SessionID:        sessionID,
				FirstPrompt:      ie.FirstPrompt,
				MessageCount:     ie.MessageCount,
				Created:          ie.Created,
				Modified:         ie.Modified,
				GitBranch:        ie.GitBranch,
				ProjectPath:      ie.ProjectPath,
				BashCommandCount: bashCount,
				ToolUseCounts:    toolCounts,
				EstimatedTokens:  tokens,
			}
			if entry.MessageCount == 0 {
				entry.MessageCount = len(messages)
			}
			return entry
		}
	}

	// Derive from messages
	var firstPrompt string
	var created, modified, gitBranch string
	for _, m := range messages {
		if m.Type == "user" {
			text := m.Message.ContentText()
			if len(text) > 200 {
				text = text[:200]
			}
			firstPrompt = text
			created = m.Timestamp
			gitBranch = m.GitBranch
			break
		}
	}
	if len(messages) > 0 {
		modified = messages[len(messages)-1].Timestamp
	}

	return model.SessionEntry{
		SessionID:        sessionID,
		FirstPrompt:      firstPrompt,
		MessageCount:     len(messages),
		Created:          created,
		Modified:         modified,
		GitBranch:        gitBranch,
		BashCommandCount: bashCount,
		ToolUseCounts:    toolCounts,
		EstimatedTokens:  tokens,
	}
}

// estimateTokens gives a rough token count based on character length.
func estimateTokens(messages []model.ConversationMessage) int {
	totalChars := 0
	for _, m := range messages {
		totalChars += len(m.Message.ContentText())
	}
	return totalChars / 4
}

func countToolUses(messages []model.ConversationMessage) map[string]int {
	counts := make(map[string]int)
	for _, m := range messages {
		if m.Message.Role != "assistant" {
			continue
		}
		for _, b := range m.Message.ContentBlocks() {
			if b.Type == "tool_use" && b.Name != "" {
				counts[b.Name]++
			}
		}
	}
	return counts
}

// decodeProjectDir converts an encoded directory name back to a path.
func decodeProjectDir(dirName string) string {
	path := dirName
	if strings.HasPrefix(path, "-") {
		path = "/" + path[1:]
	}
	path = strings.ReplaceAll(path, "--", "/.")
	path = strings.ReplaceAll(path, "-", "/")
	return path
}

// encodeProjectDir converts a path to the encoded directory name format.
func encodeProjectDir(path string) string {
	result := strings.ReplaceAll(path, "/.", "--")
	result = strings.ReplaceAll(result, "/", "-")
	return result
}
