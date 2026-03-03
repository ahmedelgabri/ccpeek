package store

import (
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

// BeginTx starts a transaction for batch writes.
func (s *Store) BeginTx() (*sqlx.Tx, error) {
	return s.db.Beginx()
}

// InsertPlan inserts a plan entry with its content.
func (s *Store) InsertPlan(tx *sqlx.Tx, p model.PlanEntry, content string) error {
	_, err := tx.Exec(
		`INSERT INTO plans (file_name, title, size_bytes, content) VALUES (?, ?, ?, ?)`,
		p.FileName, p.Title, p.SizeBytes, content,
	)
	return err
}

// InsertShellSnapshot inserts a shell snapshot entry with its content.
func (s *Store) InsertShellSnapshot(tx *sqlx.Tx, snap model.ShellSnapshotEntry, content string) error {
	_, err := tx.Exec(
		`INSERT INTO shell_snapshots (file_name, timestamp, size_bytes, content) VALUES (?, ?, ?, ?)`,
		snap.FileName, snap.Timestamp, snap.SizeBytes, content,
	)
	return err
}

// InsertProject inserts a project and returns its database ID.
func (s *Store) InsertProject(tx *sqlx.Tx, dirName, displayName string) (int64, error) {
	res, err := tx.Exec(
		`INSERT INTO projects (dir_name, display_name) VALUES (?, ?)`,
		dirName, displayName,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertSession inserts a session entry and returns its database ID.
func (s *Store) InsertSession(tx *sqlx.Tx, projectID int64, sess model.SessionEntry) (int64, error) {
	toolJSON, err := json.Marshal(sess.ToolUseCounts)
	if err != nil {
		toolJSON = []byte("{}")
	}

	res, err := tx.Exec(
		`INSERT INTO sessions (session_id, project_id, first_prompt, message_count,
		 created_at, modified_at, git_branch, project_path,
		 bash_command_count, tool_use_counts, estimated_tokens)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.SessionID, projectID, sess.FirstPrompt, sess.MessageCount,
		sess.Created, sess.Modified, sess.GitBranch, sess.ProjectPath,
		sess.BashCommandCount, string(toolJSON), sess.EstimatedTokens,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertMessages inserts conversation messages for a session.
// It also populates the FTS index with extracted text content.
func (s *Store) InsertMessages(tx *sqlx.Tx, dbSessionID int64, messages []model.ConversationMessage) error {
	msgStmt, err := tx.Prepare(
		`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing message insert: %w", err)
	}
	defer msgStmt.Close()

	ftsStmt, err := tx.Prepare(
		`INSERT INTO messages_fts (rowid, text_content) VALUES (?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing FTS insert: %w", err)
	}
	defer ftsStmt.Close()

	for i, m := range messages {
		contentJSON := string(m.Message.Content)

		res, err := msgStmt.Exec(
			dbSessionID, i, m.Type, m.Message.Role, m.Timestamp,
			m.UUID, contentJSON, m.Cwd, m.GitBranch,
		)
		if err != nil {
			return fmt.Errorf("inserting message %d: %w", i, err)
		}

		// Populate FTS with extracted text
		textContent := m.Message.ContentText()
		if textContent != "" {
			msgID, _ := res.LastInsertId()
			if _, err := ftsStmt.Exec(msgID, textContent); err != nil {
				return fmt.Errorf("inserting FTS for message %d: %w", i, err)
			}
		}
	}

	return nil
}

// InsertTodo inserts a todo entry and its items. sessionDBID can be 0 for unlinked todos.
func (s *Store) InsertTodo(tx *sqlx.Tx, entry model.TodoEntry, items []model.TodoItem, sessionDBID int64) error {
	statusJSON, err := json.Marshal(entry.Statuses)
	if err != nil {
		statusJSON = []byte("{}")
	}

	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	res, err := tx.Exec(
		`INSERT INTO todos (file_name, session_id, item_count, statuses) VALUES (?, ?, ?, ?)`,
		entry.FileName, sessID, entry.ItemCount, string(statusJSON),
	)
	if err != nil {
		return err
	}

	todoID, _ := res.LastInsertId()

	itemStmt, err := tx.Prepare(
		`INSERT INTO todo_items (todo_id, seq, content, status, active_form) VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer itemStmt.Close()

	for i, item := range items {
		if _, err := itemStmt.Exec(todoID, i, item.Content, item.Status, item.ActiveForm); err != nil {
			return err
		}
	}

	return nil
}

// InsertFileHistory inserts a file history entry and its versions.
// sessionDBID can be 0 for unlinked entries.
func (s *Store) InsertFileHistory(tx *sqlx.Tx, conversationID string, versions []model.FileVersionInfo, sessionDBID int64) error {
	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	res, err := tx.Exec(
		`INSERT INTO file_history (conversation_id, session_id, file_count) VALUES (?, ?, ?)`,
		conversationID, sessID, len(versions),
	)
	if err != nil {
		return err
	}

	fhID, _ := res.LastInsertId()

	stmt, err := tx.Prepare(
		`INSERT INTO file_versions (file_history_id, hash, version, content) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, v := range versions {
		if _, err := stmt.Exec(fhID, v.Hash, v.Version, v.Content); err != nil {
			return err
		}
	}

	return nil
}

// InsertHistory inserts a history timeline entry.
func (s *Store) InsertHistory(tx *sqlx.Tx, entry model.HistoryEntry) error {
	_, err := tx.Exec(
		`INSERT INTO history (display, timestamp, project) VALUES (?, ?, ?)`,
		entry.Display, entry.Timestamp, entry.Project,
	)
	return err
}

// SetMeta sets a metadata key-value pair.
func (s *Store) SetMeta(tx *sqlx.Tx, key, value string) error {
	_, err := tx.Exec(
		`INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`,
		key, value,
	)
	return err
}

// LinkTodoToSession links a todo to a session (sets session_id on the todo,
// and todo_file_name on the session).
func (s *Store) LinkTodoToSession(tx *sqlx.Tx, todoFileName string, sessionDBID int64) error {
	if _, err := tx.Exec(
		`UPDATE todos SET session_id = ? WHERE file_name = ?`,
		sessionDBID, todoFileName,
	); err != nil {
		return err
	}
	_, err := tx.Exec(
		`UPDATE sessions SET todo_file_name = ? WHERE id = ?`,
		todoFileName, sessionDBID,
	)
	return err
}

// LinkFileHistoryToSession links a file history entry to a session.
func (s *Store) LinkFileHistoryToSession(tx *sqlx.Tx, conversationID string, sessionDBID int64) error {
	if _, err := tx.Exec(
		`UPDATE file_history SET session_id = ? WHERE conversation_id = ?`,
		sessionDBID, conversationID,
	); err != nil {
		return err
	}
	_, err := tx.Exec(
		`UPDATE sessions SET has_file_history = 1 WHERE id = ?`,
		sessionDBID,
	)
	return err
}
