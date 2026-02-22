package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccexplore/internal/model"
)

func indexProjects(claudeDir, dataDir string) ([]model.ProjectEntry, error) {
	srcDir := filepath.Join(claudeDir, "projects")
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var projects []model.ProjectEntry

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		dirName := e.Name()
		projectDir := filepath.Join(srcDir, dirName)
		outDir := filepath.Join(dataDir, "projects", dirName)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			continue
		}

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

		var sessions []model.SessionEntry
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

			// Write parsed messages
			data, err := json.Marshal(messages)
			if err != nil {
				continue
			}
			_ = os.WriteFile(filepath.Join(outDir, sessionID+".json"), data, 0o644)

			// Build session metadata
			session := buildSessionEntry(sessionID, messages, sessionsIndex.Entries)
			sessions = append(sessions, session)
		}

		sort.Slice(sessions, func(i, j int) bool {
			ti, _ := time.Parse(time.RFC3339Nano, sessions[i].Modified)
			tj, _ := time.Parse(time.RFC3339Nano, sessions[j].Modified)
			return ti.After(tj)
		})

		displayName := decodeProjectDir(dirName)

		projects = append(projects, model.ProjectEntry{
			DirName:      dirName,
			DisplayName:  displayName,
			SessionCount: len(sessions),
			Sessions:     sessions,
		})
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].SessionCount > projects[j].SessionCount
	})

	return projects, nil
}

func buildSessionEntry(sessionID string, messages []model.ConversationMessage, indexEntries []model.SessionEntry) model.SessionEntry {
	toolCounts := countToolUses(messages)
	bashCount := toolCounts["Bash"]

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
	}
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
// e.g. "-Users-ahmed--dotfiles" -> "/Users/ahmed/.dotfiles"
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
// e.g. "/Users/ahmed/.dotfiles" -> "-Users-ahmed--dotfiles"
func encodeProjectDir(path string) string {
	result := strings.ReplaceAll(path, "/.", "--")
	result = strings.ReplaceAll(result, "/", "-")
	return result
}
