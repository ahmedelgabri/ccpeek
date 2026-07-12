package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/jmoiron/sqlx"
)

// SessionCommand is a normalized shell command call for a conversation.
type SessionCommand struct {
	Command   string `db:"command"`
	Timestamp string `db:"timestamp"`
}

// SessionToolCall is a normalized tool call for the tools view.
type SessionToolCall struct {
	Name      string `db:"name"`
	Kind      string `db:"kind"`
	Detail    string `db:"detail"`
	Timestamp string `db:"timestamp"`
}

// CodeOperation is a normalized code-oriented tool call.
type CodeOperation struct {
	Tool      string `db:"tool"`
	FilePath  string `db:"file_path"`
	Content   string `db:"content"`
	OldString string `db:"old_string"`
	Timestamp string `db:"timestamp"`
}

type extractedToolCall struct {
	Seq            int
	Timestamp      string
	ToolName       string
	ToolKind       string
	InputJSON      string
	ResultText     string
	FilePath       string
	SearchableText string
	ToolUseID      string
}

func normalizeToolKind(name string) string {
	switch name {
	case "Bash", "Shell":
		return "shell"
	case "Read":
		return "file_read"
	case "Write":
		return "file_write"
	case "Edit", "MultiEdit":
		return "file_edit"
	case "Glob", "LS":
		return "file_discovery"
	case "Grep":
		return "search"
	case "Task":
		return "task"
	}

	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "bash") || strings.Contains(lower, "shell"):
		return "shell"
	case strings.Contains(lower, "write"):
		return "file_write"
	case strings.Contains(lower, "edit"):
		return "file_edit"
	case strings.Contains(lower, "read"):
		return "file_read"
	case strings.Contains(lower, "grep") || strings.Contains(lower, "search"):
		return "search"
	case strings.Contains(lower, "glob") || lower == "ls" || strings.Contains(lower, "list"):
		return "file_discovery"
	case strings.Contains(lower, "task"):
		return "task"
	default:
		return "other"
	}
}

func toolInputJSON(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "{}"
	}
	return trimmed
}

func firstStringFromJSON(raw json.RawMessage, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := m[key].(string); ok {
			return value
		}
	}
	return ""
}

func toolCallSearchText(name string, input json.RawMessage, result string) string {
	parts := []string{name}
	block := model.ContentBlock{Input: input}
	if text := strings.TrimSpace(block.ToolInputText()); text != "" {
		parts = append(parts, text)
	}
	if text := strings.TrimSpace(result); text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
}

func extractToolCalls(messages []model.ConversationMessage) []extractedToolCall {
	calls := make([]extractedToolCall, 0)
	byID := make(map[string]int)
	seq := 0

	for _, message := range messages {
		blocks := message.Message.ContentBlocks()
		if len(blocks) == 0 {
			continue
		}

		for _, block := range blocks {
			switch block.Type {
			case "tool_use":
				if block.Name == "" {
					continue
				}
				call := extractedToolCall{
					Seq:            seq,
					Timestamp:      message.Timestamp,
					ToolName:       block.Name,
					ToolKind:       normalizeToolKind(block.Name),
					InputJSON:      toolInputJSON(block.Input),
					FilePath:       firstStringFromJSON(block.Input, "file_path", "path"),
					SearchableText: toolCallSearchText(block.Name, block.Input, ""),
					ToolUseID:      block.ID,
				}
				calls = append(calls, call)
				if block.ID != "" {
					byID[block.ID] = len(calls) - 1
				}
				seq++
			case "tool_result":
				if block.ToolUseID == "" {
					continue
				}
				idx, ok := byID[block.ToolUseID]
				if !ok {
					continue
				}
				resultText := strings.TrimSpace(block.ToolResultText())
				if resultText == "" {
					continue
				}
				if calls[idx].ResultText != "" {
					calls[idx].ResultText += "\n\n"
				}
				calls[idx].ResultText += resultText
				calls[idx].SearchableText = toolCallSearchText(calls[idx].ToolName, json.RawMessage(calls[idx].InputJSON), calls[idx].ResultText)
			}
		}
	}

	return calls
}

// InsertToolCalls inserts normalized tool call rows for a session.
func (s *Store) InsertToolCalls(ctx context.Context, tx *sqlx.Tx, dbSessionID int64, messages []model.ConversationMessage) error {
	calls := extractToolCalls(messages)
	if len(calls) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO tool_calls (
			session_id, seq, timestamp, tool_name, tool_kind, input_json, result_text, file_path, searchable_text
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing tool call insert: %w", err)
	}
	defer stmt.Close()

	for _, call := range calls {
		if _, err := stmt.ExecContext(
			ctx,
			dbSessionID,
			call.Seq,
			call.Timestamp,
			call.ToolName,
			call.ToolKind,
			call.InputJSON,
			call.ResultText,
			call.FilePath,
			call.SearchableText,
		); err != nil {
			return fmt.Errorf("inserting tool call %d: %w", call.Seq, err)
		}
	}

	return nil
}

func (s *Store) backfillToolCalls(ctx context.Context) error {
	actualRows := []struct {
		SessionID int64 `db:"session_id"`
		Count     int   `db:"cnt"`
	}{}
	if err := s.db.SelectContext(ctx, &actualRows, `SELECT session_id, COUNT(*) AS cnt FROM tool_calls GROUP BY session_id`); err != nil {
		return err
	}
	actualCounts := make(map[int64]int, len(actualRows))
	for _, row := range actualRows {
		actualCounts[row.SessionID] = row.Count
	}

	rows, err := s.db.QueryxContext(ctx, `
		SELECT session_id, role, timestamp, content
		FROM messages
		ORDER BY session_id, seq`)
	if err != nil {
		return err
	}
	defer rows.Close()

	needRebuild := false
	var currentSessionID int64 = -1
	messages := make([]model.ConversationMessage, 0)
	check := func() {
		if currentSessionID < 0 {
			return
		}
		expected := len(extractToolCalls(messages))
		if actualCounts[currentSessionID] != expected {
			needRebuild = true
		}
	}

	for rows.Next() {
		var row struct {
			SessionID int64  `db:"session_id"`
			Role      string `db:"role"`
			Timestamp string `db:"timestamp"`
			Content   string `db:"content"`
		}
		if err := rows.StructScan(&row); err != nil {
			return err
		}
		if row.SessionID != currentSessionID {
			check()
			currentSessionID = row.SessionID
			messages = messages[:0]
		}
		messages = append(messages, model.ConversationMessage{
			Timestamp: row.Timestamp,
			Message: model.MessagePayload{
				Role:    row.Role,
				Content: []byte(row.Content),
			},
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	check()

	if !needRebuild {
		return nil
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM tool_calls`); err != nil {
		return err
	}

	rows, err = tx.QueryxContext(ctx, `
		SELECT session_id, role, timestamp, content
		FROM messages
		ORDER BY session_id, seq`)
	if err != nil {
		return err
	}
	defer rows.Close()

	currentSessionID = -1
	messages = messages[:0]
	flush := func() error {
		if currentSessionID < 0 {
			return nil
		}
		return s.InsertToolCalls(ctx, tx, currentSessionID, messages)
	}

	for rows.Next() {
		var row struct {
			SessionID int64  `db:"session_id"`
			Role      string `db:"role"`
			Timestamp string `db:"timestamp"`
			Content   string `db:"content"`
		}
		if err := rows.StructScan(&row); err != nil {
			return err
		}
		if row.SessionID != currentSessionID {
			if err := flush(); err != nil {
				return err
			}
			currentSessionID = row.SessionID
			messages = messages[:0]
		}
		messages = append(messages, model.ConversationMessage{
			Timestamp: row.Timestamp,
			Message: model.MessagePayload{
				Role:    row.Role,
				Content: []byte(row.Content),
			},
		})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}

	return tx.Commit()
}

// GetSessionCommands returns normalized shell commands for a session.
func (s *Store) GetSessionCommands(ctx context.Context, dirName, sessionID string) ([]SessionCommand, error) {
	var rows []SessionCommand
	err := s.db.SelectContext(ctx, &rows, `
		SELECT COALESCE(json_extract(tc.input_json, '$.command'), '') AS command,
		       tc.timestamp
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE p.dir_name = ? AND s.session_id = ? AND tc.tool_kind = 'shell'
		ORDER BY tc.seq`, dirName, sessionID)
	return rows, err
}

// GetSessionToolCalls returns normalized tool calls for the tools view.
func (s *Store) GetSessionToolCalls(ctx context.Context, dirName, sessionID string) ([]SessionToolCall, error) {
	var rows []SessionToolCall
	err := s.db.SelectContext(ctx, &rows, `
		SELECT tc.tool_name AS name,
		       tc.tool_kind AS kind,
		       COALESCE(
			   CASE tc.tool_name
			       WHEN 'Bash' THEN json_extract(tc.input_json, '$.command')
			       WHEN 'Read' THEN json_extract(tc.input_json, '$.file_path')
			       WHEN 'Write' THEN json_extract(tc.input_json, '$.file_path')
			       WHEN 'Edit' THEN json_extract(tc.input_json, '$.file_path')
			       WHEN 'Glob' THEN json_extract(tc.input_json, '$.pattern')
			       WHEN 'Grep' THEN json_extract(tc.input_json, '$.pattern')
			       WHEN 'Task' THEN json_extract(tc.input_json, '$.description')
			       ELSE NULL
			   END,
			   json_extract(tc.input_json, '$.command'),
			   json_extract(tc.input_json, '$.file_path'),
			   json_extract(tc.input_json, '$.path'),
			   json_extract(tc.input_json, '$.pattern'),
			   json_extract(tc.input_json, '$.description'),
			   json_extract(tc.input_json, '$.query'),
			   json_extract(tc.input_json, '$.url'),
			   ''
		       ) AS detail,
		       tc.timestamp
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE p.dir_name = ? AND s.session_id = ?
		ORDER BY tc.seq`, dirName, sessionID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Detail = truncateStr(rows[i].Detail, 120)
	}
	return rows, nil
}

// GetSessionToolStats returns per-tool usage counts for a session.
func (s *Store) GetSessionToolStats(ctx context.Context, dirName, sessionID string) ([]ToolUsageStat, error) {
	var stats []ToolUsageStat
	err := s.db.SelectContext(ctx, &stats, `
		SELECT tc.tool_name AS name, COUNT(*) AS count
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE p.dir_name = ? AND s.session_id = ?
		GROUP BY tc.tool_name
		ORDER BY count DESC, name`, dirName, sessionID)
	if err != nil {
		return nil, err
	}
	if len(stats) > 0 {
		maxCount := stats[0].Count
		for i := range stats {
			stats[i].Percent = float64(stats[i].Count) / float64(maxCount) * 100
		}
	}
	return stats, nil
}

// GetSessionCodeOperations returns normalized code-writing/editing operations for a session.
func (s *Store) GetSessionCodeOperations(ctx context.Context, dirName, sessionID string) ([]CodeOperation, error) {
	var rows []struct {
		Tool      string `db:"tool"`
		FilePath  string `db:"file_path"`
		InputJSON string `db:"input_json"`
		Timestamp string `db:"timestamp"`
	}
	err := s.db.SelectContext(ctx, &rows, `
		SELECT tc.tool_name AS tool,
		       tc.file_path,
		       tc.input_json,
		       tc.timestamp
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE p.dir_name = ? AND s.session_id = ? AND tc.tool_kind IN ('file_write', 'file_edit')
		ORDER BY tc.seq`, dirName, sessionID)
	if err != nil {
		return nil, err
	}

	ops := make([]CodeOperation, 0, len(rows))
	for _, row := range rows {
		var input struct {
			Content   string `json:"content"`
			NewString string `json:"new_string"`
			OldString string `json:"old_string"`
			Edits     []struct {
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			} `json:"edits"`
		}
		_ = json.Unmarshal([]byte(row.InputJSON), &input)

		if row.Tool == "MultiEdit" && len(input.Edits) > 0 {
			for _, edit := range input.Edits {
				ops = append(ops, CodeOperation{
					Tool:      row.Tool,
					FilePath:  row.FilePath,
					Content:   edit.NewString,
					OldString: edit.OldString,
					Timestamp: row.Timestamp,
				})
			}
			continue
		}

		content := input.Content
		if content == "" {
			content = input.NewString
		}
		ops = append(ops, CodeOperation{
			Tool:      row.Tool,
			FilePath:  row.FilePath,
			Content:   content,
			OldString: input.OldString,
			Timestamp: row.Timestamp,
		})
	}
	return ops, nil
}
