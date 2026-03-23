package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

// BeginTx starts a transaction for batch writes.
func (s *Store) BeginTx(ctx context.Context) (*sqlx.Tx, error) {
	return s.db.BeginTxx(ctx, nil)
}

// InsertPlan inserts a plan entry with its content.
func (s *Store) InsertPlan(ctx context.Context, tx *sqlx.Tx, p model.PlanEntry, content, sourcePath string) error {
	source := p.Source
	if source == "" {
		source = model.SourceClaudeCode
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO plans (file_name, title, size_bytes, content, updated_at_ms, source, source_path) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.FileName, p.Title, p.SizeBytes, content, p.UpdatedAt, source, sourcePath,
	)
	return err
}

// InsertShellSnapshot inserts a shell snapshot entry with its content.
func (s *Store) InsertShellSnapshot(ctx context.Context, tx *sqlx.Tx, snap model.ShellSnapshotEntry, content, sourcePath string) error {
	source := snap.Source
	if source == "" {
		source = model.SourceClaudeCode
	}
	kind := snap.Kind
	if kind == "" {
		kind = "shell"
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO shell_snapshots (file_name, timestamp, size_bytes, content, kind, project_path, commit_hash, detail_file, source, source_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.FileName, snap.Timestamp, snap.SizeBytes, content, kind, snap.ProjectPath, snap.CommitHash, snap.DetailFile, source, sourcePath,
	)
	return err
}

// InsertProject inserts a project and returns its database ID.
func (s *Store) InsertProject(ctx context.Context, tx *sqlx.Tx, dirName, displayName, canonicalPath string) (int64, error) {
	return s.InsertProjectWithMeta(ctx, tx, dirName, displayName, canonicalPath, model.SourceClaudeCode, 0)
}

// InsertProjectWithMeta inserts a project with source/updated metadata and returns its ID.
func (s *Store) InsertProjectWithMeta(ctx context.Context, tx *sqlx.Tx, dirName, displayName, canonicalPath, source string, updatedAt int64) (int64, error) {
	if displayName == "" {
		displayName = dirName
	}
	if source == "" {
		source = model.SourceClaudeCode
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO projects (dir_name, display_name, canonical_path, source, updated_at_ms) VALUES (?, ?, ?, ?, ?)`,
		dirName, displayName, canonicalPath, source, updatedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpsertProject inserts or updates a project and returns its database ID.
func (s *Store) UpsertProject(ctx context.Context, tx *sqlx.Tx, dirName, displayName, canonicalPath string, updateDisplayName bool) (int64, error) {
	return s.UpsertProjectWithMeta(ctx, tx, dirName, displayName, canonicalPath, model.SourceClaudeCode, 0, updateDisplayName)
}

// UpsertProjectWithMeta inserts or updates a project with source/updated metadata and returns its ID.
// If updateDisplayName is false, an existing project's display_name is preserved.
func (s *Store) UpsertProjectWithMeta(ctx context.Context, tx *sqlx.Tx, dirName, displayName, canonicalPath, source string, updatedAt int64, updateDisplayName bool) (int64, error) {
	if displayName == "" {
		displayName = dirName
	}
	if source == "" {
		source = model.SourceClaudeCode
	}
	var err error
	if updateDisplayName {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO projects (dir_name, display_name, canonical_path, source, updated_at_ms) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(dir_name) DO UPDATE SET
			 	display_name = excluded.display_name,
			 	canonical_path = CASE
			 		WHEN excluded.canonical_path <> '' THEN excluded.canonical_path
			 		ELSE projects.canonical_path
			 	END,
			 	source = excluded.source,
			 	updated_at_ms = MAX(projects.updated_at_ms, excluded.updated_at_ms)`,
			dirName, displayName, canonicalPath, source, updatedAt,
		)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO projects (dir_name, display_name, canonical_path, source, updated_at_ms) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(dir_name) DO UPDATE SET
			 	canonical_path = CASE
			 		WHEN excluded.canonical_path <> '' THEN excluded.canonical_path
			 		ELSE projects.canonical_path
			 	END,
			 	source = excluded.source,
			 	updated_at_ms = MAX(projects.updated_at_ms, excluded.updated_at_ms)`,
			dirName, displayName, canonicalPath, source, updatedAt,
		)
	}
	if err != nil {
		return 0, err
	}
	var id int64
	err = tx.GetContext(ctx, &id, `SELECT id FROM projects WHERE dir_name = ?`, dirName)
	return id, err
}

// InsertSession inserts a session entry and returns its database ID.
func (s *Store) InsertSession(ctx context.Context, tx *sqlx.Tx, projectID int64, sess model.SessionEntry, sourcePath string) (int64, error) {
	toolJSON, err := json.Marshal(sess.ToolUseCounts)
	if err != nil {
		toolJSON = []byte("{}")
	}
	source := sess.Source
	if source == "" {
		source = model.SourceClaudeCode
	}
	metadataOnly := 0
	if sess.MetadataOnly {
		metadataOnly = 1
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (session_id, project_id, first_prompt, message_count,
		 created_at, modified_at, git_branch, project_path,
		 bash_command_count, tool_use_counts, estimated_tokens, metadata_only, model_name, source, source_path)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.SessionID, projectID, sess.FirstPrompt, sess.MessageCount,
		sess.Created, sess.Modified, sess.GitBranch, sess.ProjectPath,
		sess.BashCommandCount, string(toolJSON), sess.EstimatedTokens, metadataOnly, sess.ModelName, source, sourcePath,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// InsertMessages inserts conversation messages for a session.
// It also populates the FTS index with extracted text content.
func (s *Store) InsertMessages(ctx context.Context, tx *sqlx.Tx, dbSessionID int64, messages []model.ConversationMessage) error {
	msgStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO messages (session_id, seq, type, role, timestamp, uuid, content, cwd, git_branch)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing message insert: %w", err)
	}
	defer msgStmt.Close()

	ftsStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO messages_fts (rowid, text_content) VALUES (?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing FTS insert: %w", err)
	}
	defer ftsStmt.Close()

	for i, m := range messages {
		contentJSON := string(m.Message.Content)

		res, err := msgStmt.ExecContext(ctx,
			dbSessionID, i, m.Type, m.Message.Role, m.Timestamp,
			m.UUID, contentJSON, m.Cwd, m.GitBranch,
		)
		if err != nil {
			return fmt.Errorf("inserting message %d: %w", i, err)
		}

		// Populate FTS with extracted searchable text
		textContent := m.Message.SearchText()
		if textContent != "" {
			msgID, _ := res.LastInsertId()
			if _, err := ftsStmt.ExecContext(ctx, msgID, textContent); err != nil {
				return fmt.Errorf("inserting FTS for message %d: %w", i, err)
			}
		}
	}

	return nil
}

// InsertCommands inserts extracted bash commands for a session.
func (s *Store) InsertCommands(ctx context.Context, tx *sqlx.Tx, dbSessionID int64, messages []model.ConversationMessage) error {
	source := model.SourceClaudeCode
	_ = tx.GetContext(ctx, &source, `SELECT source FROM sessions WHERE id = ?`, dbSessionID)
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO commands (session_id, seq, command, timestamp, source) VALUES (?, ?, ?, ?, ?)`,
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
				if _, err := stmt.ExecContext(ctx, dbSessionID, seq, input.Command, m.Timestamp, source); err != nil {
					return fmt.Errorf("inserting command %d: %w", seq, err)
				}
				seq++
			}
		}
	}
	return nil
}

// InsertTodo inserts a todo entry and its items. sessionDBID can be 0 for unlinked todos.
func (s *Store) InsertTodo(ctx context.Context, tx *sqlx.Tx, entry model.TodoEntry, items []model.TodoItem, sessionDBID int64, sourcePath string) error {
	statusJSON, err := json.Marshal(entry.Statuses)
	if err != nil {
		statusJSON = []byte("{}")
	}

	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}
	source := entry.Source
	if source == "" {
		source = model.SourceClaudeCode
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO todos (file_name, session_id, item_count, statuses, updated_at_ms, source, source_path) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.FileName, sessID, entry.ItemCount, string(statusJSON), entry.UpdatedAt, source, sourcePath,
	)
	if err != nil {
		return err
	}

	todoID, _ := res.LastInsertId()

	itemStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO todo_items (todo_id, seq, content, status, active_form) VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer itemStmt.Close()

	for i, item := range items {
		if _, err := itemStmt.ExecContext(ctx, todoID, i, item.Content, item.Status, item.ActiveForm); err != nil {
			return err
		}
	}

	return nil
}

// InsertFileHistory inserts a file history entry and its versions.
// sessionDBID can be 0 for unlinked entries.
func (s *Store) InsertFileHistory(ctx context.Context, tx *sqlx.Tx, conversationID string, versions []model.FileVersionInfo, sessionDBID int64, sourcePath string) error {
	return s.InsertFileHistoryWithMeta(ctx, tx, conversationID, versions, sessionDBID, 0, model.SourceClaudeCode, sourcePath)
}

// InsertFileHistoryWithMeta inserts a file history entry with source/recency metadata.
func (s *Store) InsertFileHistoryWithMeta(ctx context.Context, tx *sqlx.Tx, conversationID string, versions []model.FileVersionInfo, sessionDBID int64, updatedAt int64, source, sourcePath string) error {
	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}
	if source == "" {
		source = model.SourceClaudeCode
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO file_history (conversation_id, session_id, file_count, updated_at_ms, source, source_path) VALUES (?, ?, ?, ?, ?, ?)`,
		conversationID, sessID, len(versions), updatedAt, source, sourcePath,
	)
	if err != nil {
		return err
	}

	fhID, _ := res.LastInsertId()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO file_versions (file_history_id, hash, version, content, file_path, change_kind, patch, timestamp) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, v := range versions {
		if _, err := stmt.ExecContext(ctx, fhID, v.Hash, v.Version, v.Content, v.FilePath, v.ChangeKind, v.Patch, v.Timestamp); err != nil {
			return err
		}
	}

	return nil
}

// InsertHistory inserts a history timeline entry.
func (s *Store) InsertHistory(ctx context.Context, tx *sqlx.Tx, entry model.HistoryEntry, sourcePath string) error {
	source := entry.Source
	if source == "" {
		source = model.SourceClaudeCode
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO history (display, timestamp, project, project_dir, source, source_path) VALUES (?, ?, ?, ?, ?, ?)`,
		entry.Display, entry.Timestamp, entry.Project, entry.ProjectDir, source, sourcePath,
	)
	return err
}

// InsertTaskGroup inserts a task group and its items. sessionDBID can be 0 for unlinked groups.
func (s *Store) InsertTaskGroup(ctx context.Context, tx *sqlx.Tx, entry model.TaskGroupEntry, items []model.TaskItem, sessionDBID int64, sourcePath string) error {
	statusJSON, err := json.Marshal(entry.Statuses)
	if err != nil {
		statusJSON = []byte("{}")
	}

	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO task_groups (dir_name, session_id, item_count, statuses, source_path) VALUES (?, ?, ?, ?, ?)`,
		entry.DirName, sessID, entry.ItemCount, string(statusJSON), sourcePath,
	)
	if err != nil {
		return err
	}

	groupID, _ := res.LastInsertId()

	itemStmt, err := tx.PrepareContext(ctx,
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
		if _, err := itemStmt.ExecContext(ctx, groupID, i, item.ID, item.Subject, item.Description, item.ActiveForm, item.Status, string(blocksJSON), string(blockedByJSON)); err != nil {
			return err
		}
	}

	return nil
}

// InsertPasteCache inserts a paste-cache entry with its content.
func (s *Store) InsertPasteCache(ctx context.Context, tx *sqlx.Tx, entry model.PasteCacheEntry, content, sourcePath string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO paste_cache (file_name, size_bytes, content, source_path) VALUES (?, ?, ?, ?)`,
		entry.FileName, entry.SizeBytes, content, sourcePath,
	)
	return err
}

// InsertMemory inserts a memory .md entry. projectID can be nil for unlinked memories.
func (s *Store) InsertMemory(ctx context.Context, tx *sqlx.Tx, projectDir, fileName string, projectID *int64, sizeBytes int64, content, sourcePath string) error {
	var pid any
	if projectID != nil {
		pid = *projectID
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO memories (project_dir, file_name, project_id, size_bytes, content, source, source_path) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectDir, fileName, pid, sizeBytes, content, model.SourceClaudeCode, sourcePath,
	)
	return err
}

// InsertUsageFacet inserts a usage-data facet. sessionDBID can be 0 for unlinked facets.
func (s *Store) InsertUsageFacet(ctx context.Context, tx *sqlx.Tx, entry model.UsageFacetEntry, sessionDBID int64, sourcePath string) error {
	goalJSON, _ := json.Marshal(entry.GoalCategories)
	satJSON, _ := json.Marshal(entry.Satisfaction)
	fricJSON, _ := json.Marshal(entry.FrictionCounts)

	var sessID any
	if sessionDBID > 0 {
		sessID = sessionDBID
	}

	_, err := tx.ExecContext(ctx,
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
func (s *Store) InsertUsageReport(ctx context.Context, tx *sqlx.Tx, content, sourcePath string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO usage_report (content, source_path) VALUES (?, ?)`, content, sourcePath)
	return err
}

// SetMeta sets a metadata key-value pair.
func (s *Store) SetMeta(ctx context.Context, tx *sqlx.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO meta (key, value) VALUES (?, ?)`,
		key, value,
	)
	return err
}

// LinkTodoToSession links a todo to a session (sets session_id on the todo,
// and todo_file_name on the session).
func (s *Store) LinkTodoToSession(ctx context.Context, tx *sqlx.Tx, todoFileName string, sessionDBID int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE todos SET session_id = ? WHERE file_name = ?`,
		sessionDBID, todoFileName,
	); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE sessions SET todo_file_name = ? WHERE id = ?`,
		todoFileName, sessionDBID,
	)
	return err
}

// SetSourceFileHash records the content hash for a source file.
func (s *Store) SetSourceFileHash(ctx context.Context, tx *sqlx.Tx, path, contentHash, indexedAt string) error {
	q := `INSERT OR REPLACE INTO source_files (path, content_hash, indexed_at) VALUES (?, ?, ?)`
	if tx != nil {
		_, err := tx.ExecContext(ctx, q, path, contentHash, indexedAt)
		return err
	}
	_, err := s.db.ExecContext(ctx, q, path, contentHash, indexedAt)
	return err
}

// LinkFileHistoryToSession links a file history entry to a session.
func (s *Store) LinkFileHistoryToSession(ctx context.Context, tx *sqlx.Tx, conversationID string, sessionDBID int64) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE file_history SET session_id = ? WHERE conversation_id = ?`,
		sessionDBID, conversationID,
	); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		`UPDATE sessions SET has_file_history = 1 WHERE id = ?`,
		sessionDBID,
	)
	return err
}

// allowedTables restricts which table names can be used in dynamic SQL.
var allowedTables = map[string]bool{
	"plans": true, "shell_snapshots": true, "history": true,
	"paste_cache": true, "usage_facets": true, "usage_report": true,
	"memories": true, "todos": true, "todo_items": true,
	"file_history": true, "file_versions": true,
	"task_groups": true, "task_items": true,
}

// DeleteBySource deletes all rows from a table where source_path matches.
func (s *Store) DeleteBySource(ctx context.Context, tx *sqlx.Tx, table, sourcePath string) error {
	if !allowedTables[table] {
		return fmt.Errorf("disallowed table name: %s", table)
	}
	_, err := tx.ExecContext(ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE source_path = ?`, table),
		sourcePath,
	)
	return err
}

// DeleteSessionCascade deletes sessions and their message FTS rows for a given source_path.
// Child rows and nullable links are handled by foreign key ON DELETE actions.
func (s *Store) DeleteSessionCascade(ctx context.Context, tx *sqlx.Tx, sourcePath string) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM messages_fts
		WHERE rowid IN (
			SELECT m.id
			FROM messages m
			JOIN sessions s ON m.session_id = s.id
			WHERE s.source_path = ?
		)`, sourcePath); err != nil {
		return fmt.Errorf("deleting FTS for source %s: %w", sourcePath, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE source_path = ?`, sourcePath); err != nil {
		return err
	}

	return nil
}

// PruneOrphanedProjects removes projects that have no remaining sessions.
func (s *Store) PruneOrphanedProjects(ctx context.Context, tx *sqlx.Tx) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id NOT IN (SELECT DISTINCT project_id FROM sessions)`)
	return err
}

// RebuildFTS drops and rebuilds the FTS index from existing messages.
func (s *Store) RebuildFTS(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS messages_fts`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE messages_fts USING fts5(text_content)`); err != nil {
		return err
	}
	// Repopulate from messages — we store the raw JSON content, so we need
	// to extract text. Since ContentText() is Go-side, we do a simple
	// approach: insert the content column directly (it's JSON but FTS will
	// still tokenize the text portions for search).
	// For proper extraction, the caller should repopulate via Go code.
	return nil
}

// ClearScanFindings removes all non-ignored scan findings.
// Ignored findings are preserved across re-scans.
func (s *Store) ClearScanFindings(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scan_findings WHERE ignored = 0`)
	return err
}

// InsertScanFinding inserts a single scan finding.
// Skips insertion if an ignored finding with the same identity already exists.
// Returns true if the finding was inserted, false if it was skipped.
func (s *Store) InsertScanFinding(ctx context.Context, tx *sqlx.Tx, f model.ScanFinding) (bool, error) {
	var exists int
	err := tx.GetContext(ctx, &exists,
		`SELECT COUNT(*) FROM scan_findings
		 WHERE rule_id = ? AND source_type = ? AND source_id = ? AND ignored = 1`,
		f.RuleID, f.SourceType, f.SourceID,
	)
	if err == nil && exists > 0 {
		return false, nil
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO scan_findings (rule_id, description, source_type, source_id, match_redacted, line_number, scanned_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.RuleID, f.Description, f.SourceType, f.SourceID, f.MatchRedacted, f.Line, f.ScannedAt,
	)
	return err == nil, err
}

// RepopulateFTS fills the FTS table from all existing messages.
// Must be called after RebuildFTS.
func (s *Store) RepopulateFTS(ctx context.Context, tx *sqlx.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, content FROM messages`)
	if err != nil {
		return err
	}
	defer rows.Close()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO messages_fts (rowid, text_content) VALUES (?, ?)`)
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
		text := msg.SearchText()
		if text != "" {
			if _, err := stmt.ExecContext(ctx, id, text); err != nil {
				return err
			}
		}
	}

	return rows.Err()
}
