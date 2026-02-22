package model

import (
	"encoding/json"
)

// IndexData is the top-level metadata written to index.json.
type IndexData struct {
	GeneratedAt    string               `json:"generatedAt"`
	Plans          []PlanEntry          `json:"plans"`
	ShellSnapshots []ShellSnapshotEntry `json:"shellSnapshots"`
	Todos          []TodoEntry          `json:"todos"`
	Projects       []ProjectEntry       `json:"projects"`
	FileHistory    []FileHistoryEntry   `json:"fileHistory"`
	History        []HistoryEntry       `json:"history"`
}

type PlanEntry struct {
	FileName  string `json:"fileName"`
	Title     string `json:"title"`
	SizeBytes int64  `json:"sizeBytes"`
}

type ShellSnapshotEntry struct {
	FileName  string `json:"fileName"`
	Timestamp int64  `json:"timestamp"`
	SizeBytes int64  `json:"sizeBytes"`
}

type TodoEntry struct {
	FileName    string         `json:"fileName"`
	ItemCount   int            `json:"itemCount"`
	Statuses    map[string]int `json:"statuses"`
	SessionID   string         `json:"sessionId,omitempty"`
	ProjectDir  string         `json:"projectDir,omitempty"`
	ProjectName string         `json:"projectName,omitempty"`
}

type ProjectEntry struct {
	DirName      string         `json:"dirName"`
	DisplayName  string         `json:"displayName"`
	SessionCount int            `json:"sessionCount"`
	Sessions     []SessionEntry `json:"sessions"`
}

type SessionEntry struct {
	SessionID        string         `json:"sessionId"`
	FirstPrompt      string         `json:"firstPrompt"`
	MessageCount     int            `json:"messageCount"`
	Created          string         `json:"created"`
	Modified         string         `json:"modified"`
	GitBranch        string         `json:"gitBranch,omitempty"`
	ProjectPath      string         `json:"projectPath,omitempty"`
	TodoFileName     string         `json:"todoFileName,omitempty"`
	HasFileHistory   bool           `json:"hasFileHistory,omitempty"`
	BashCommandCount int            `json:"bashCommandCount,omitempty"`
	ToolUseCounts    map[string]int `json:"toolUseCounts,omitempty"`
	EstimatedTokens  int            `json:"estimatedTokens,omitempty"`
}

type FileHistoryEntry struct {
	ConversationID string `json:"conversationId"`
	FileCount      int    `json:"fileCount"`
	ProjectDir     string `json:"projectDir,omitempty"`
	ProjectName    string `json:"projectName,omitempty"`
}

type HistoryEntry struct {
	Display   string `json:"display"`
	Timestamp int64  `json:"timestamp"`
	Project   string `json:"project"`
}

// ConversationMessage represents a single message in a session JSONL.
type ConversationMessage struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	UUID      string         `json:"uuid"`
	Message   MessagePayload `json:"message"`
	SessionID string         `json:"sessionId,omitempty"`
	Cwd       string         `json:"cwd,omitempty"`
	GitBranch string         `json:"gitBranch,omitempty"`
}

// MessagePayload holds the role and content of a message.
// Content can be a JSON string or an array of ContentBlock.
type MessagePayload struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// ContentText returns the content as a plain string if it is one,
// or concatenates all text blocks.
func (m *MessagePayload) ContentText() string {
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err == nil {
		var text string
		for _, b := range blocks {
			if b.Type == "text" {
				text += b.Text
			}
		}
		return text
	}

	return ""
}

// ContentBlocks returns the content parsed as a slice of ContentBlock.
// Returns nil if content is a plain string.
func (m *MessagePayload) ContentBlocks() []ContentBlock {
	var blocks []ContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err == nil {
		return blocks
	}
	return nil
}

// IsString returns true if the content is a plain JSON string.
func (m *MessagePayload) IsString() bool {
	var s string
	return json.Unmarshal(m.Content, &s) == nil
}

// ContentBlock represents a single block within message content.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// ToolResultText extracts plain text from a tool_result content field,
// which can be a string or an array of {type, text} objects.
func (b *ContentBlock) ToolResultText() string {
	if b.Content == nil {
		return ""
	}

	var s string
	if err := json.Unmarshal(b.Content, &s); err == nil {
		return s
	}

	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b.Content, &parts); err == nil {
		var result string
		for _, p := range parts {
			if p.Text != "" {
				if result != "" {
					result += "\n"
				}
				result += p.Text
			}
		}
		return result
	}

	return ""
}

// TodoItem represents a single item in a todo list file.
type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"activeForm,omitempty"`
}

// FileHistoryDetail is the detail data for a single conversation's file history.
type FileHistoryDetail struct {
	ConversationID string            `json:"conversationId"`
	Files          []FileVersionInfo `json:"files"`
}

type FileVersionInfo struct {
	Hash    string `json:"hash"`
	Version int    `json:"version"`
	Content string `json:"content"`
}

// RawJSONLLine is the shape of raw lines from Claude's conversation JSONL files.
type RawJSONLLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	UUID      string          `json:"uuid,omitempty"`
	Message   *MessagePayload `json:"message,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Cwd       string          `json:"cwd,omitempty"`
	GitBranch string          `json:"gitBranch,omitempty"`
}

// SessionsIndex is the structure of sessions-index.json files.
type SessionsIndex struct {
	Entries []SessionEntry `json:"entries"`
}
