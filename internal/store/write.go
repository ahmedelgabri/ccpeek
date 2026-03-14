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
func (s *Store) InsertPlan(tx *sqlx.Tx, p model.PlanEntry, content, sourcePath string) error {
	_, err := tx.Exec(
		`INSERT INTO plans (file_name, title, size_bytes, content, source_path) VALUES (?, ?, ?, ?, ?)`,
		p.FileName, p.Title, p.SizeBytes, content, sourcePath,
	)
	return err
}

// InsertShellSnapshot inserts a shell snapshot entry with its content.
func (s *Store) InsertShellSnapshot(tx *sqlx.Tx, snap model.ShellSnapshotEntry, content, sourcePath string) error {
	_, err := tx.Exec(
		`INSERT INTO shell_snapshots (file_name, timestamp, size_bytes, content, source_path) VALUES (?, ?, ?, ?, ?)`,
		snap.FileName, snap.Timestamp, snap.SizeBytes, content, sourcePath,
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

// UpsertProject inserts or updates a project and returns its database ID.
func (s *Store) UpsertProject(tx *sqlx.Tx, dirName, displayName string) (int64, error) {
	_, err := tx.Exec(
		`INSERT INTO projects (dir_name, display_name) VALUES (?, ?)
		 ON CONFLICT(dir_name) DO UPDATE SET display_name = excluded.display_name`,
		dirName, displayName,
	)
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.Get(&id, `SELECT id FROM projects WHERE dir_name = ?`, dirName)
	return id, err
}

// InsertSession inserts a session entry and returns its database ID.
func (s *Store) InsertSession(tx *sqlx.Tx, projectID int64, sess model.SessionEntry, sourcePath string) (int64, error) {
	toolJSON, err := json.Marshal(sess.ToolUseCounts)
	if err != nil {
		toolJSON = []byte("{}")
	}

	res, err := tx.Exec(
		`INSERT INTO sessions (session_id, project_id, first_prompt, message_count,
		 created_at, modified_at, git_branch, project_path,
		 bash_command_count, tool_use_counts, estimated_tokens, source_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.SessionID, projectID, sess.FirstPrompt, sess.MessageCount,
		sess.Created, sess.Modified, sess.GitBranch, sess.ProjectPath,
		sess.BashCommandCount, string(toolJSON), sess.EstimatedTokens, sourcePath,
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

// InsertCommands inserts extracted bash commands for a session.
func (s *Store) InsertCommands(tx *sqlx.Tx, dbSessionID int64, messages []model.ConversationMessage) error {
	stmt, err := tx.Prepare(
		`INSERT INTO commands (session_id, seq, command, timestamp) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing command insert: %w", err)
	}
	defer stmt.Close()

	seq := 0
	for _, m := range messages {
		if m.Message.Role != "assistant" {
			continue
		}
		for _, b := range m.Message.ContentBlocks() {
			if b.Type != "tool_use" || b.Name != "Bash" {
				continue
			}
			var input struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &input) == nil && input.Command != "" {
				if _, err := stmt.Exec(dbSessionID, seq, input.Command, m.Timestamp); err != nil {
					return fmt.Errorf("inserting command %d: %w", seq, err)
				}
				seq++
			}
		}
	}
	return nil
}

// InsertTodo inserts a todo entry and its items. sessionDBID can be 0 for unlinked todos.
func (s *Store) InsertTodo(tx *sqlx.Tx, entry model.TodoEntry, items []model.TodoItem, sessionDBID int64, sourcePath string) error {
	statusJSON, err := json.Marshal(entry.Statuses)
	if err != nil {
		statusJSON = []byte("{}")
	}

	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	res, err := tx.Exec(
		`INSERT INTO todos (file_name, session_id, item_count, statuses, source_path) VALUES (?, ?, ?, ?, ?)`,
		entry.FileName, sessID, entry.ItemCount, string(statusJSON), sourcePath,
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
func (s *Store) InsertFileHistory(tx *sqlx.Tx, conversationID string, versions []model.FileVersionInfo, sessionDBID int64, sourcePath string) error {
	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	res, err := tx.Exec(
		`INSERT INTO file_history (conversation_id, session_id, file_count, source_path) VALUES (?, ?, ?, ?)`,
		conversationID, sessID, len(versions), sourcePath,
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
func (s *Store) InsertHistory(tx *sqlx.Tx, entry model.HistoryEntry, sourcePath string) error {
	_, err := tx.Exec(
		`INSERT INTO history (display, timestamp, project, source_path) VALUES (?, ?, ?, ?)`,
		entry.Display, entry.Timestamp, entry.Project, sourcePath,
	)
	return err
}

// InsertTaskGroup inserts a task group and its items. sessionDBID can be 0 for unlinked groups.
func (s *Store) InsertTaskGroup(tx *sqlx.Tx, entry model.TaskGroupEntry, items []model.TaskItem, sessionDBID int64, sourcePath string) error {
	statusJSON, err := json.Marshal(entry.Statuses)
	if err != nil {
		statusJSON = []byte("{}")
	}

	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	res, err := tx.Exec(
		`INSERT INTO task_groups (dir_name, session_id, item_count, statuses, source_path) VALUES (?, ?, ?, ?, ?)`,
		entry.DirName, sessID, entry.ItemCount, string(statusJSON), sourcePath,
	)
	if err != nil {
		return err
	}

	groupID, _ := res.LastInsertId()

	itemStmt, err := tx.Prepare(
		`INSERT INTO task_items (task_group_id, seq, item_id, subject, description, active_form, status, blocks, blocked_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer itemStmt.Close()

	for i, item := range items {
		blocksJSON, _ := json.Marshal(item.Blocks)
		blockedByJSON, _ := json.Marshal(item.BlockedBy)
		if _, err := itemStmt.Exec(groupID, i, item.ID, item.Subject, item.Description, item.ActiveForm, item.Status, string(blocksJSON), string(blockedByJSON)); err != nil {
			return err
		}
	}

	return nil
}

// InsertPasteCache inserts a paste-cache entry with its content.
func (s *Store) InsertPasteCache(tx *sqlx.Tx, entry model.PasteCacheEntry, content, sourcePath string) error {
	_, err := tx.Exec(
		`INSERT INTO paste_cache (file_name, size_bytes, content, source_path) VALUES (?, ?, ?, ?)`,
		entry.FileName, entry.SizeBytes, content, sourcePath,
	)
	return err
}

// InsertMemory inserts a MEMORY.md entry. projectID can be nil for unlinked memories.
func (s *Store) InsertMemory(tx *sqlx.Tx, projectDir string, projectID *int64, sizeBytes int64, content, sourcePath string) error {
	var pid any
	if projectID != nil {
		pid = *projectID
	}
	_, err := tx.Exec(
		`INSERT INTO memories (project_dir, project_id, size_bytes, content, source_path) VALUES (?, ?, ?, ?, ?)`,
		projectDir, pid, sizeBytes, content, sourcePath,
	)
	return err
}

// InsertUsageFacet inserts a usage-data facet. sessionDBID can be 0 for unlinked facets.
func (s *Store) InsertUsageFacet(tx *sqlx.Tx, entry model.UsageFacetEntry, sessionDBID int64, sourcePath string) error {
	goalJSON, _ := json.Marshal(entry.GoalCategories)
	satJSON, _ := json.Marshal(entry.Satisfaction)
	fricJSON, _ := json.Marshal(entry.FrictionCounts)

	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	_, err := tx.Exec(
		`INSERT INTO usage_facets (session_id_text, db_session_id, underlying_goal, outcome, helpfulness,
		 session_type, primary_success, brief_summary, friction_detail, goal_categories, satisfaction, friction_counts, source_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.SessionID, sessID, entry.UnderlyingGoal, entry.Outcome, entry.Helpfulness,
		entry.SessionType, entry.PrimarySuccess, entry.BriefSummary, entry.FrictionDetail,
		string(goalJSON), string(satJSON), string(fricJSON), sourcePath,
	)
	return err
}

// InsertUsageReport inserts the usage-data report HTML content.
func (s *Store) InsertUsageReport(tx *sqlx.Tx, content, sourcePath string) error {
	_, err := tx.Exec(`INSERT INTO usage_report (content, source_path) VALUES (?, ?)`, content, sourcePath)
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

// SetSourceFileHash records the content hash for a source file.
func (s *Store) SetSourceFileHash(tx *sqlx.Tx, path, contentHash, indexedAt string) error {
	q := `INSERT OR REPLACE INTO source_files (path, content_hash, indexed_at) VALUES (?, ?, ?)`
	if tx != nil {
		_, err := tx.Exec(q, path, contentHash, indexedAt)
		return err
	}
	_, err := s.db.Exec(q, path, contentHash, indexedAt)
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

// DeleteBySource deletes all rows from a table where source_path matches.
// For tables with child rows (todos→todo_items, etc.), the caller must
// delete children first.
func (s *Store) DeleteBySource(tx *sqlx.Tx, table, sourcePath string) error {
	_, err := tx.Exec(
		fmt.Sprintf(`DELETE FROM %s WHERE source_path = ?`, table),
		sourcePath,
	)
	return err
}

// DeleteChildrenBySource deletes child rows whose parent has a given source_path.
// parentTable is the parent table, parentIDCol is its PK column,
// childTable is the child table, childFKCol is the FK column in the child.
func (s *Store) DeleteChildrenBySource(tx *sqlx.Tx, parentTable, parentIDCol, childTable, childFKCol, sourcePath string) error {
	_, err := tx.Exec(
		fmt.Sprintf(`DELETE FROM %s WHERE %s IN (SELECT %s FROM %s WHERE source_path = ?)`,
			childTable, childFKCol, parentIDCol, parentTable),
		sourcePath,
	)
	return err
}

// DeleteSessionCascade deletes sessions and their messages/FTS for a given source_path.
// Also clears session linkage on todos and file_history that reference deleted sessions.
func (s *Store) DeleteSessionCascade(tx *sqlx.Tx, sourcePath string) error {
	// Collect session IDs being deleted
	var sessionIDs []int64
	if err := tx.Select(&sessionIDs,
		`SELECT id FROM sessions WHERE source_path = ?`, sourcePath); err != nil {
		return err
	}

	if len(sessionIDs) == 0 {
		return nil
	}

	for _, sid := range sessionIDs {
		// Delete FTS entries for these messages
		if _, err := tx.Exec(
			`DELETE FROM messages_fts WHERE rowid IN (SELECT id FROM messages WHERE session_id = ?)`, sid,
		); err != nil {
			return fmt.Errorf("deleting FTS for session %d: %w", sid, err)
		}
		// Delete messages
		if _, err := tx.Exec(`DELETE FROM messages WHERE session_id = ?`, sid); err != nil {
			return fmt.Errorf("deleting messages for session %d: %w", sid, err)
		}
		// Delete commands
		if _, err := tx.Exec(`DELETE FROM commands WHERE session_id = ?`, sid); err != nil {
			return fmt.Errorf("deleting commands for session %d: %w", sid, err)
		}
		// Unlink todos (set session_id to NULL, keep the todo)
		if _, err := tx.Exec(`UPDATE todos SET session_id = NULL WHERE session_id = ?`, sid); err != nil {
			return fmt.Errorf("unlinking todos for session %d: %w", sid, err)
		}
		// Unlink file_history
		if _, err := tx.Exec(`UPDATE file_history SET session_id = NULL WHERE session_id = ?`, sid); err != nil {
			return fmt.Errorf("unlinking file_history for session %d: %w", sid, err)
		}
		// Unlink task_groups
		if _, err := tx.Exec(`UPDATE task_groups SET session_id = NULL WHERE session_id = ?`, sid); err != nil {
			return fmt.Errorf("unlinking task_groups for session %d: %w", sid, err)
		}
		// Unlink usage_facets
		if _, err := tx.Exec(`UPDATE usage_facets SET db_session_id = NULL WHERE db_session_id = ?`, sid); err != nil {
			return fmt.Errorf("unlinking usage_facets for session %d: %w", sid, err)
		}
	}

	// Delete the sessions themselves
	if _, err := tx.Exec(`DELETE FROM sessions WHERE source_path = ?`, sourcePath); err != nil {
		return err
	}

	return nil
}

// PruneOrphanedProjects removes projects that have no remaining sessions.
func (s *Store) PruneOrphanedProjects(tx *sqlx.Tx) error {
	_, err := tx.Exec(`DELETE FROM projects WHERE id NOT IN (SELECT DISTINCT project_id FROM sessions)`)
	return err
}

// RebuildFTS drops and rebuilds the FTS index from existing messages.
func (s *Store) RebuildFTS(tx *sqlx.Tx) error {
	if _, err := tx.Exec(`DROP TABLE IF EXISTS messages_fts`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE messages_fts USING fts5(text_content)`); err != nil {
		return err
	}
	// Repopulate from messages — we store the raw JSON content, so we need
	// to extract text. Since ContentText() is Go-side, we do a simple
	// approach: insert the content column directly (it's JSON but FTS will
	// still tokenize the text portions for search).
	// For proper extraction, the caller should repopulate via Go code.
	return nil
}

// RepopulateFTS fills the FTS table from all existing messages.
// Must be called after RebuildFTS.
func (s *Store) RepopulateFTS(tx *sqlx.Tx) error {
	rows, err := tx.Query(`SELECT id, content FROM messages`)
	if err != nil {
		return err
	}
	defer rows.Close()

	stmt, err := tx.Prepare(`INSERT INTO messages_fts (rowid, text_content) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for rows.Next() {
		var id int64
		var contentJSON string
		if err := rows.Scan(&id, &contentJSON); err != nil {
			return err
		}

		msg := model.MessagePayload{}
		msg.Content = []byte(contentJSON)
		text := msg.ContentText()
		if text != "" {
			if _, err := stmt.Exec(id, text); err != nil {
				return err
			}
		}
	}

	return rows.Err()
}
