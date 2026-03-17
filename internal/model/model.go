package model

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
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
	Display   string `json:"display" db:"display"`
	Timestamp int64  `json:"timestamp" db:"timestamp"`
	Project   string `json:"project" db:"project"`
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
	Content    string `json:"content" db:"content"`
	Status     string `json:"status" db:"status"`
	ActiveForm string `json:"activeForm,omitempty" db:"activeform"`
}

// FileHistoryDetail is the detail data for a single conversation's file history.
type FileHistoryDetail struct {
	ConversationID string            `json:"conversationId"`
	Files          []FileVersionInfo `json:"files"`
}

type FileVersionInfo struct {
	Hash    string `json:"hash" db:"hash"`
	Version int    `json:"version" db:"version"`
	Content string `json:"content" db:"content"`
}

// TaskGroupEntry represents a task group directory under ~/.claude/tasks/.
type TaskGroupEntry struct {
	DirName     string         `json:"dirName"`
	ItemCount   int            `json:"itemCount"`
	Statuses    map[string]int `json:"statuses"`
	SessionID   string         `json:"sessionId,omitempty"`
	ProjectDir  string         `json:"projectDir,omitempty"`
	ProjectName string         `json:"projectName,omitempty"`
}

// TaskItem represents a single task item within a task group.
type TaskItem struct {
	ID          string   `json:"id" db:"item_id"`
	Subject     string   `json:"subject" db:"subject"`
	Description string   `json:"description" db:"description"`
	ActiveForm  string   `json:"activeForm,omitempty" db:"active_form"`
	Status      string   `json:"status" db:"status"`
	Blocks      []string `json:"blocks,omitempty"`
	BlockedBy   []string `json:"blockedBy,omitempty"`
}

// PasteCacheEntry represents a paste-cache file.
type PasteCacheEntry struct {
	FileName  string `json:"fileName"`
	SizeBytes int64  `json:"sizeBytes"`
	Preview   string `json:"preview"`
}

// MemoryEntry represents a project's MEMORY.md file.
type MemoryEntry struct {
	ProjectDir  string `json:"projectDir"`
	ProjectName string `json:"projectName"`
	SizeBytes   int64  `json:"sizeBytes"`
	Preview     string `json:"preview"`
}

// UsageFacetEntry represents a usage-data facet for a session.
type UsageFacetEntry struct {
	SessionID      string         `json:"sessionId"`
	UnderlyingGoal string         `json:"underlyingGoal"`
	Outcome        string         `json:"outcome"`
	Helpfulness    string         `json:"claudeHelpfulness"`
	SessionType    string         `json:"sessionType"`
	PrimarySuccess string         `json:"primarySuccess"`
	BriefSummary   string         `json:"briefSummary"`
	FrictionDetail string         `json:"frictionDetail"`
	GoalCategories map[string]int `json:"goalCategories"`
	Satisfaction   map[string]int `json:"userSatisfactionCounts"`
	FrictionCounts map[string]int `json:"frictionCounts"`
	ProjectDir     string         `json:"projectDir,omitempty"`
	ProjectName    string         `json:"projectName,omitempty"`
}

// CommandEntry represents a bash command extracted from a session.
type CommandEntry struct {
	Command        string `json:"command" db:"command"`
	Timestamp      string `json:"timestamp" db:"timestamp"`
	SessionID      string `json:"sessionId" db:"session_id"`
	FirstPrompt    string `json:"firstPrompt" db:"first_prompt"`
	ProjectDirName string `json:"projectDirName" db:"dir_name"`
	ProjectDisplay string `json:"projectDisplay" db:"display_name"`
}

// FormatCommands writes commands to w in the given shell history format.
// Supported formats: "zsh", "bash", "fish", "plain".
func FormatCommands(w io.Writer, commands []CommandEntry, format string) error {
	switch format {
	case "plain", "bash", "zsh", "fish":
	default:
		return fmt.Errorf("unsupported format %q: use plain, bash, zsh, or fish", format)
	}
	for _, cmd := range commands {
		switch format {
		case "zsh":
			ts := parseTimestampUnix(cmd.Timestamp)
			fmt.Fprintf(w, ": %d:0;%s\n", ts, escapeZshMultiline(cmd.Command))
		case "fish":
			ts := parseTimestampUnix(cmd.Timestamp)
			fmt.Fprintf(w, "- cmd: %s\n  when: %s\n", cmd.Command, strconv.FormatInt(ts, 10))
		default: // "plain" or "bash"
			fmt.Fprintf(w, "%s\n", cmd.Command)
		}
	}
	return nil
}

// DecodeProjectDir converts an encoded directory name back to a path.
func DecodeProjectDir(dirName string) string {
	path := dirName
	if strings.HasPrefix(path, "-") {
		path = "/" + path[1:]
	}
	path = strings.ReplaceAll(path, "--", "/.")
	path = strings.ReplaceAll(path, "-", "/")
	return path
}

// EncodeProjectDir converts a path to the encoded directory name format.
func EncodeProjectDir(path string) string {
	result := strings.ReplaceAll(path, "/.", "--")
	result = strings.ReplaceAll(result, "/", "-")
	return result
}

// escapeZshMultiline escapes newlines for zsh extended history format.
// Non-empty continuation lines end with \\ and empty lines end with \.
func escapeZshMultiline(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(line)
		if i < len(lines)-1 {
			if line == "" {
				b.WriteString("\\\n")
			} else {
				b.WriteString("\\\\\n")
			}
		}
	}
	return b.String()
}

func parseTimestampUnix(ts string) int64 {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z"} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Unix()
		}
	}
	return 0
}

// ScanFinding represents a secret or sensitive value detected during scanning.
type ScanFinding struct {
	ID             int64  `json:"id" db:"id"`
	RuleID         string `json:"ruleId" db:"rule_id"`
	Description    string `json:"description" db:"description"`
	SourceType     string `json:"sourceType" db:"source_type"` // message, command, plan, shell_snapshot, paste_cache, memory
	SourceID       string `json:"sourceId" db:"source_id"`     // identifies the record within source_type
	MatchRedacted  string `json:"matchRedacted" db:"match_redacted"`
	Line           int    `json:"line" db:"line_number"`
	ScannedAt      string `json:"scannedAt" db:"scanned_at"`
	Ignored        bool   `json:"ignored" db:"ignored"`
	ProjectDirName string `json:"projectDirName,omitempty" db:"project_dir"`
	ProjectDisplay string `json:"projectDisplay,omitempty" db:"project_name"`
	SessionID      string `json:"sessionId,omitempty" db:"session_id_text"`
}

// SourceURL returns a link to the source of this finding, or empty string.
// For message and command findings, SourceID may contain "sessionID@timestamp"
// to enable deep linking to the specific message or command.
func (f *ScanFinding) SourceURL() string {
	switch f.SourceType {
	case "message":
		sessionID, ts := splitSourceID(f.SourceID)
		dir := f.ProjectDirName
		if dir == "" || sessionID == "" {
			return ""
		}
		url := "/projects/" + dir + "/" + sessionID + "/"
		if ts != "" {
			url += "#" + anchorize("msg", ts)
		}
		return url
	case "command":
		sessionID, ts := splitSourceID(f.SourceID)
		dir := f.ProjectDirName
		if dir != "" && sessionID != "" {
			url := "/projects/" + dir + "/" + sessionID + "/commands/"
			if ts != "" {
				url += "#" + anchorize("cmd", ts)
			}
			return url
		}
		// Fallback for old-format findings (SourceID = DB id)
		if dir != "" && f.SessionID != "" {
			return "/projects/" + dir + "/" + f.SessionID + "/commands/"
		}
	case "plan":
		name := f.SourceID
		if len(name) > 3 && name[len(name)-3:] == ".md" {
			name = name[:len(name)-3]
		}
		return "/plans/" + name + "/"
	case "shell_snapshot":
		name := f.SourceID
		if len(name) > 3 && name[len(name)-3:] == ".sh" {
			name = name[:len(name)-3]
		}
		return "/shell-snapshots/" + name + "/"
	case "paste_cache":
		name := f.SourceID
		if len(name) > 4 && name[len(name)-4:] == ".txt" {
			name = name[:len(name)-4]
		}
		return "/paste-cache/" + name + "/"
	case "memory":
		return "/memories/" + f.SourceID + "/"
	}
	return ""
}

// splitSourceID splits "sessionID@timestamp" into its parts.
// If no "@" is present, returns the whole string as sessionID with empty timestamp.
func splitSourceID(s string) (sessionID, timestamp string) {
	if i := strings.LastIndex(s, "@"); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// anchorize builds a URL-safe fragment id from a prefix and value.
func anchorize(prefix, value string) string {
	safe := strings.NewReplacer(":", "-", ".", "-").Replace(value)
	return prefix + "-" + safe
}

// ScanStats holds aggregate counts for the scan results page.
type ScanStats struct {
	TotalFindings  int            `json:"totalFindings"`
	FindingsByRule map[string]int `json:"findingsByRule"`
	FindingsByType map[string]int `json:"findingsByType"`
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
