package model

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
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
	DirName       string         `json:"dirName"`
	DisplayName   string         `json:"displayName"`
	CanonicalPath string         `json:"canonicalPath,omitempty"`
	SessionCount  int            `json:"sessionCount"`
	Sessions      []SessionEntry `json:"sessions"`
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
	Display        string `json:"display" db:"display"`
	Timestamp      int64  `json:"timestamp" db:"timestamp"`
	Project        string `json:"project" db:"project"`
	ProjectDirName string `json:"projectDirName,omitempty"`
	ProjectDisplay string `json:"projectDisplay,omitempty"`
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

// SearchText returns text suitable for search/scan indexing.
// Unlike ContentText, it includes tool_result text and text extracted
// from tool_use inputs.
func (m *MessagePayload) SearchText() string {
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(m.Content, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if t := strings.TrimSpace(b.Text); t != "" {
					parts = append(parts, t)
				}
			case "tool_result":
				if t := strings.TrimSpace(b.ToolResultText()); t != "" {
					parts = append(parts, t)
				}
			case "tool_use":
				if t := strings.TrimSpace(b.ToolInputText()); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
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

// ToolInputText extracts text-bearing string values from a tool_use input.
func (b *ContentBlock) ToolInputText() string {
	if b.Input == nil {
		return ""
	}

	var value any
	if err := json.Unmarshal(b.Input, &value); err != nil {
		return ""
	}

	var parts []string
	collectJSONStrings(value, &parts)
	return strings.Join(parts, "\n")
}

func collectJSONStrings(v any, parts *[]string) {
	switch x := v.(type) {
	case string:
		if strings.TrimSpace(x) != "" {
			*parts = append(*parts, x)
		}
	case []any:
		for _, item := range x {
			collectJSONStrings(item, parts)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectJSONStrings(x[k], parts)
		}
	}
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

// MemoryEntry represents a .md file inside a project's memory folder.
type MemoryEntry struct {
	ProjectDir  string `json:"projectDir"`
	FileName    string `json:"fileName"`
	ProjectName string `json:"projectName"`
	SizeBytes   int64  `json:"sizeBytes"`
	Preview     string `json:"preview"`
}

// MemoryProjectGroup groups memory entries by project for the list view.
type MemoryProjectGroup struct {
	ProjectDir  string        `json:"projectDir"`
	ProjectName string        `json:"projectName"`
	FileCount   int           `json:"fileCount"`
	TotalBytes  int64         `json:"totalBytes"`
	Entries     []MemoryEntry `json:"entries"`
}

// NormalizeMemoryFileName ensures a memory file name ends with .md.
func NormalizeMemoryFileName(fileName string) string {
	if fileName == "" {
		return ""
	}
	if strings.HasSuffix(fileName, ".md") {
		return fileName
	}
	return fileName + ".md"
}

// MemoryFileSlug returns a route slug for a memory file (without .md).
func MemoryFileSlug(fileName string) string {
	return strings.TrimSuffix(NormalizeMemoryFileName(fileName), ".md")
}

// MemorySourceID builds the canonical scan/search source ID for a memory file.
func MemorySourceID(projectDir, fileName string) string {
	name := NormalizeMemoryFileName(fileName)
	if name == "" {
		return projectDir
	}
	return projectDir + "/" + name
}

// MemoryURL returns the canonical detail URL for a memory file.
func MemoryURL(projectDir, fileName string) string {
	slug := MemoryFileSlug(fileName)
	if slug == "" {
		return "/memories/" + url.PathEscape(projectDir) + "/"
	}
	return "/memories/" + url.PathEscape(projectDir) + "/" + url.PathEscape(slug) + "/"
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

// ValidateCommandFormat validates a shell-history export format.
func ValidateCommandFormat(format string) error {
	switch format {
	case "plain", "bash", "zsh", "fish":
		return nil
	default:
		return fmt.Errorf("unsupported format %q: use plain, bash, zsh, or fish", format)
	}
}

// WriteCommand writes a single command in the given shell history format.
func WriteCommand(w io.Writer, cmd CommandEntry, format string) error {
	if err := ValidateCommandFormat(format); err != nil {
		return err
	}

	switch format {
	case "zsh":
		ts := parseTimestampUnix(cmd.Timestamp)
		_, err := fmt.Fprintf(w, ": %d:0;%s\n", ts, escapeZshMultiline(cmd.Command))
		return err
	case "fish":
		ts := parseTimestampUnix(cmd.Timestamp)
		_, err := fmt.Fprintf(w, "- cmd: %s\n  when: %s\n", cmd.Command, strconv.FormatInt(ts, 10))
		return err
	default: // "plain" or "bash"
		_, err := fmt.Fprintf(w, "%s\n", cmd.Command)
		return err
	}
}

// FormatCommands writes commands to w in the given shell history format.
// Supported formats: "zsh", "bash", "fish", "plain".
func FormatCommands(w io.Writer, commands []CommandEntry, format string) error {
	if err := ValidateCommandFormat(format); err != nil {
		return err
	}
	for _, cmd := range commands {
		if err := WriteCommand(w, cmd, format); err != nil {
			return err
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
	SourceType     string `json:"sourceType" db:"source_type"` // message, command, plan, shell_snapshot, paste_cache, memory, todo, task, file_history, usage_facet, usage_report
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
		// SourceID is usually "projectDir/fileName" (e.g. "-Users-proj/MEMORY.md").
		if i := strings.Index(f.SourceID, "/"); i > 0 {
			return MemoryURL(f.SourceID[:i], f.SourceID[i+1:])
		}
		// Legacy findings without a file name map to the default MEMORY.md file.
		return MemoryURL(f.SourceID, "MEMORY.md")
	case "todo":
		name, anchor := splitSourceFragment(f.SourceID)
		url := "/todos/" + strings.TrimSuffix(name, ".json") + "/"
		if anchor != "" {
			url += "#" + anchor
		}
		return url
	case "task":
		dir, anchor := splitSourceFragment(f.SourceID)
		url := "/tasks/" + dir + "/"
		if anchor != "" {
			url += "#" + anchor
		}
		return url
	case "file_history":
		return "/file-history/" + f.SourceID + "/"
	case "usage_facet":
		return "/usage-data/" + f.SourceID + "/"
	case "usage_report":
		return "/usage-data/report/"
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

// splitSourceFragment splits "id#fragment" into its parts.
func splitSourceFragment(s string) (id, fragment string) {
	if i := strings.LastIndex(s, "#"); i > 0 {
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

// IngestRun records the outcome of an indexing or pruning run.
type IngestRun struct {
	ID              int64  `json:"id" db:"id"`
	Mode            string `json:"mode" db:"mode"`
	Status          string `json:"status" db:"status"`
	ClaudeDir       string `json:"claudeDir" db:"claude_dir"`
	StartedAt       string `json:"startedAt" db:"started_at"`
	FinishedAt      string `json:"finishedAt" db:"finished_at"`
	DurationMS      int64  `json:"durationMs" db:"duration_ms"`
	FilesSeen       int    `json:"filesSeen" db:"files_seen"`
	FilesChanged    int    `json:"filesChanged" db:"files_changed"`
	RecordsIndexed  int    `json:"recordsIndexed" db:"records_indexed"`
	SkippedFiles    int    `json:"skippedFiles" db:"skipped_files"`
	SkippedRows     int    `json:"skippedRows" db:"skipped_rows"`
	ParseFailures   int    `json:"parseFailures" db:"parse_failures"`
	UnresolvedLinks int    `json:"unresolvedLinks" db:"unresolved_links"`
	WarningCount    int    `json:"warningCount" db:"warning_count"`
	ErrorMessage    string `json:"errorMessage,omitempty" db:"error_message"`
}

// IngestIssue records a non-fatal issue encountered during ingest.
type IngestIssue struct {
	ID         int64  `json:"id" db:"id"`
	RunID      int64  `json:"runId" db:"run_id"`
	Severity   string `json:"severity" db:"severity"`
	Category   string `json:"category" db:"category"`
	SourceType string `json:"sourceType" db:"source_type"`
	SourcePath string `json:"sourcePath" db:"source_path"`
	LineNumber int    `json:"lineNumber,omitempty" db:"line_number"`
	Detail     string `json:"detail" db:"detail"`
	CreatedAt  string `json:"createdAt" db:"created_at"`
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
