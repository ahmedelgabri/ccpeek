package store

import (
	"database/sql"
	"encoding/json"
	"sort"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

// Stats holds aggregate counts for the dashboard.
type Stats struct {
	ProjectCount   int `db:"projectcount"`
	SessionCount   int `db:"sessioncount"`
	PlanCount      int `db:"plancount"`
	SnapshotCount  int `db:"snapshotcount"`
	TodoCount      int `db:"todocount"`
	FileHistCount  int `db:"filehistcount"`
	TaskGroupCount int `db:"taskgroupcount"`
	PasteCacheCount int `db:"pastecachecount"`
	UsageFacetCount int `db:"usagefacetcount"`
}

// GetStats returns aggregate counts for the dashboard using a single query.
func (s *Store) GetStats() (Stats, error) {
	var st Stats
	err := s.db.Get(&st, `
		SELECT
			(SELECT COUNT(*) FROM projects) AS projectcount,
			(SELECT COUNT(*) FROM sessions) AS sessioncount,
			(SELECT COUNT(*) FROM plans) AS plancount,
			(SELECT COUNT(*) FROM shell_snapshots) AS snapshotcount,
			(SELECT COUNT(*) FROM todos) AS todocount,
			(SELECT COUNT(*) FROM file_history) AS filehistcount,
			(SELECT COUNT(*) FROM task_groups) AS taskgroupcount,
			(SELECT COUNT(*) FROM paste_cache) AS pastecachecount,
			(SELECT COUNT(*) FROM usage_facets) AS usagefacetcount`)
	return st, err
}

// ListPlans returns all plans ordered by file name.
func (s *Store) ListPlans() ([]model.PlanEntry, error) {
	var rows []struct {
		FileName  string `db:"file_name"`
		Title     string `db:"title"`
		SizeBytes int64  `db:"size_bytes"`
	}
	if err := s.db.Select(&rows, `SELECT file_name, title, size_bytes FROM plans ORDER BY file_name`); err != nil {
		return nil, err
	}
	plans := make([]model.PlanEntry, len(rows))
	for i, r := range rows {
		plans[i] = model.PlanEntry{FileName: r.FileName, Title: r.Title, SizeBytes: r.SizeBytes}
	}
	return plans, nil
}

// GetPlan returns a plan entry and its content.
func (s *Store) GetPlan(fileNameWithoutExt string) (*model.PlanEntry, string, error) {
	var row struct {
		FileName  string `db:"file_name"`
		Title     string `db:"title"`
		SizeBytes int64  `db:"size_bytes"`
		Content   string `db:"content"`
	}
	err := s.db.Get(&row,
		`SELECT file_name, title, size_bytes, content FROM plans
		 WHERE file_name = ? OR REPLACE(file_name, '.md', '') = ?`,
		fileNameWithoutExt+".md", fileNameWithoutExt,
	)
	if err != nil {
		return nil, "", err
	}
	entry := &model.PlanEntry{FileName: row.FileName, Title: row.Title, SizeBytes: row.SizeBytes}
	return entry, row.Content, nil
}

// ListShellSnapshots returns all snapshots sorted newest first.
func (s *Store) ListShellSnapshots() ([]model.ShellSnapshotEntry, error) {
	var rows []struct {
		FileName  string `db:"file_name"`
		Timestamp int64  `db:"timestamp"`
		SizeBytes int64  `db:"size_bytes"`
	}
	if err := s.db.Select(&rows, `SELECT file_name, timestamp, size_bytes FROM shell_snapshots ORDER BY timestamp DESC`); err != nil {
		return nil, err
	}
	snaps := make([]model.ShellSnapshotEntry, len(rows))
	for i, r := range rows {
		snaps[i] = model.ShellSnapshotEntry{FileName: r.FileName, Timestamp: r.Timestamp, SizeBytes: r.SizeBytes}
	}
	return snaps, nil
}

// GetShellSnapshot returns a snapshot entry and its content.
func (s *Store) GetShellSnapshot(fileNameWithoutExt string) (*model.ShellSnapshotEntry, string, error) {
	var row struct {
		FileName  string `db:"file_name"`
		Timestamp int64  `db:"timestamp"`
		SizeBytes int64  `db:"size_bytes"`
		Content   string `db:"content"`
	}
	err := s.db.Get(&row,
		`SELECT file_name, timestamp, size_bytes, content FROM shell_snapshots
		 WHERE file_name = ? OR REPLACE(file_name, '.sh', '') = ?`,
		fileNameWithoutExt+".sh", fileNameWithoutExt,
	)
	if err != nil {
		return nil, "", err
	}
	entry := &model.ShellSnapshotEntry{FileName: row.FileName, Timestamp: row.Timestamp, SizeBytes: row.SizeBytes}
	return entry, row.Content, nil
}

// ListTodos returns all todo entries.
func (s *Store) ListTodos() ([]model.TodoEntry, error) {
	var rows []struct {
		FileName    string         `db:"file_name"`
		ItemCount   int            `db:"item_count"`
		Statuses    string         `db:"statuses"`
		SessionID   sql.NullString `db:"session_id_text"`
		ProjectDir  sql.NullString `db:"project_dir"`
		ProjectName sql.NullString `db:"project_name"`
	}
	err := s.db.Select(&rows, `
		SELECT t.file_name, t.item_count, t.statuses,
			   s.session_id AS session_id_text,
			   p.dir_name AS project_dir,
			   p.display_name AS project_name
		FROM todos t
		LEFT JOIN sessions s ON t.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		ORDER BY t.file_name`)
	if err != nil {
		return nil, err
	}
	todos := make([]model.TodoEntry, len(rows))
	for i, r := range rows {
		statuses := make(map[string]int)
		_ = json.Unmarshal([]byte(r.Statuses), &statuses)
		todos[i] = model.TodoEntry{
			FileName:    r.FileName,
			ItemCount:   r.ItemCount,
			Statuses:    statuses,
			SessionID:   r.SessionID.String,
			ProjectDir:  r.ProjectDir.String,
			ProjectName: r.ProjectName.String,
		}
	}
	return todos, nil
}

// GetTodo returns a todo entry by filename (without .json extension).
func (s *Store) GetTodo(fileNameWithoutExt string) (*model.TodoEntry, []model.TodoItem, error) {
	var todoRow struct {
		ID          int64          `db:"id"`
		FileName    string         `db:"file_name"`
		ItemCount   int            `db:"item_count"`
		Statuses    string         `db:"statuses"`
		SessionID   sql.NullString `db:"session_id_text"`
		ProjectDir  sql.NullString `db:"project_dir"`
		ProjectName sql.NullString `db:"project_name"`
	}
	err := s.db.Get(&todoRow, `
		SELECT t.id, t.file_name, t.item_count, t.statuses,
			   s.session_id AS session_id_text,
			   p.dir_name AS project_dir,
			   p.display_name AS project_name
		FROM todos t
		LEFT JOIN sessions s ON t.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		WHERE t.file_name = ? OR REPLACE(t.file_name, '.json', '') = ?`,
		fileNameWithoutExt+".json", fileNameWithoutExt,
	)
	if err != nil {
		return nil, nil, err
	}

	statuses := make(map[string]int)
	_ = json.Unmarshal([]byte(todoRow.Statuses), &statuses)
	entry := &model.TodoEntry{
		FileName:    todoRow.FileName,
		ItemCount:   todoRow.ItemCount,
		Statuses:    statuses,
		SessionID:   todoRow.SessionID.String,
		ProjectDir:  todoRow.ProjectDir.String,
		ProjectName: todoRow.ProjectName.String,
	}

	var items []model.TodoItem
	err = s.db.Select(&items, `
		SELECT content, status, active_form AS activeform
		FROM todo_items WHERE todo_id = ? ORDER BY seq`, todoRow.ID)
	if err != nil {
		return entry, nil, err
	}
	return entry, items, nil
}

// ListProjects returns all projects with their sessions (sorted by session count desc).
// Uses a single query to load all sessions and groups them by project in Go,
// avoiding N+1 queries.
func (s *Store) ListProjects() ([]model.ProjectEntry, error) {
	var projRows []struct {
		ID          int64  `db:"id"`
		DirName     string `db:"dir_name"`
		DisplayName string `db:"display_name"`
	}
	if err := s.db.Select(&projRows, `SELECT id, dir_name, display_name FROM projects ORDER BY (SELECT COUNT(*) FROM sessions WHERE project_id = projects.id) DESC`); err != nil {
		return nil, err
	}

	// Batch-load all sessions in one query, grouped by project_id.
	var allSessRows []struct {
		ProjectID        int64  `db:"project_id"`
		SessionID        string `db:"session_id"`
		FirstPrompt      string `db:"first_prompt"`
		MessageCount     int    `db:"message_count"`
		CreatedAt        string `db:"created_at"`
		ModifiedAt       string `db:"modified_at"`
		GitBranch        string `db:"git_branch"`
		ProjectPath      string `db:"project_path"`
		TodoFileName     string `db:"todo_file_name"`
		HasFileHistory   int    `db:"has_file_history"`
		BashCommandCount int    `db:"bash_command_count"`
		ToolUseCounts    string `db:"tool_use_counts"`
		EstimatedTokens  int    `db:"estimated_tokens"`
	}
	if err := s.db.Select(&allSessRows, `
		SELECT project_id, session_id, first_prompt, message_count, created_at, modified_at,
			   git_branch, project_path, todo_file_name, has_file_history,
			   bash_command_count, tool_use_counts, estimated_tokens
		FROM sessions ORDER BY modified_at DESC`); err != nil {
		return nil, err
	}

	// Group sessions by project_id.
	sessMap := make(map[int64][]model.SessionEntry)
	for _, r := range allSessRows {
		toolCounts := make(map[string]int)
		_ = json.Unmarshal([]byte(r.ToolUseCounts), &toolCounts)
		sessMap[r.ProjectID] = append(sessMap[r.ProjectID], model.SessionEntry{
			SessionID:        r.SessionID,
			FirstPrompt:      r.FirstPrompt,
			MessageCount:     r.MessageCount,
			Created:          r.CreatedAt,
			Modified:         r.ModifiedAt,
			GitBranch:        r.GitBranch,
			ProjectPath:      r.ProjectPath,
			TodoFileName:     r.TodoFileName,
			HasFileHistory:   r.HasFileHistory != 0,
			BashCommandCount: r.BashCommandCount,
			ToolUseCounts:    toolCounts,
			EstimatedTokens:  r.EstimatedTokens,
		})
	}

	projects := make([]model.ProjectEntry, len(projRows))
	for i, pr := range projRows {
		sessions := sessMap[pr.ID]
		projects[i] = model.ProjectEntry{
			DirName:      pr.DirName,
			DisplayName:  pr.DisplayName,
			SessionCount: len(sessions),
			Sessions:     sessions,
		}
	}
	return projects, nil
}

// GetProject returns a single project with its sessions.
func (s *Store) GetProject(dirName string) (*model.ProjectEntry, error) {
	var pr struct {
		ID          int64  `db:"id"`
		DirName     string `db:"dir_name"`
		DisplayName string `db:"display_name"`
	}
	if err := s.db.Get(&pr, `SELECT id, dir_name, display_name FROM projects WHERE dir_name = ?`, dirName); err != nil {
		return nil, err
	}
	sessions, err := s.listSessionsForProject(pr.ID)
	if err != nil {
		return nil, err
	}
	return &model.ProjectEntry{
		DirName:      pr.DirName,
		DisplayName:  pr.DisplayName,
		SessionCount: len(sessions),
		Sessions:     sessions,
	}, nil
}

func (s *Store) listSessionsForProject(projectID int64) ([]model.SessionEntry, error) {
	var rows []struct {
		SessionID        string `db:"session_id"`
		FirstPrompt      string `db:"first_prompt"`
		MessageCount     int    `db:"message_count"`
		CreatedAt        string `db:"created_at"`
		ModifiedAt       string `db:"modified_at"`
		GitBranch        string `db:"git_branch"`
		ProjectPath      string `db:"project_path"`
		TodoFileName     string `db:"todo_file_name"`
		HasFileHistory   int    `db:"has_file_history"`
		BashCommandCount int    `db:"bash_command_count"`
		ToolUseCounts    string `db:"tool_use_counts"`
		EstimatedTokens  int    `db:"estimated_tokens"`
	}
	if err := s.db.Select(&rows, `
		SELECT session_id, first_prompt, message_count, created_at, modified_at,
			   git_branch, project_path, todo_file_name, has_file_history,
			   bash_command_count, tool_use_counts, estimated_tokens
		FROM sessions WHERE project_id = ? ORDER BY modified_at DESC`, projectID); err != nil {
		return nil, err
	}
	sessions := make([]model.SessionEntry, len(rows))
	for i, r := range rows {
		toolCounts := make(map[string]int)
		_ = json.Unmarshal([]byte(r.ToolUseCounts), &toolCounts)
		sessions[i] = model.SessionEntry{
			SessionID:        r.SessionID,
			FirstPrompt:      r.FirstPrompt,
			MessageCount:     r.MessageCount,
			Created:          r.CreatedAt,
			Modified:         r.ModifiedAt,
			GitBranch:        r.GitBranch,
			ProjectPath:      r.ProjectPath,
			TodoFileName:     r.TodoFileName,
			HasFileHistory:   r.HasFileHistory != 0,
			BashCommandCount: r.BashCommandCount,
			ToolUseCounts:    toolCounts,
			EstimatedTokens:  r.EstimatedTokens,
		}
	}
	return sessions, nil
}

// SessionFilter holds optional filter/sort parameters for listing sessions.
type SessionFilter struct {
	Sort   string // "oldest", "messages", "tokens" (default: newest first)
	Branch string // filter by git branch (exact match)
	From   string // filter by created_at >= (ISO date)
	To     string // filter by created_at <= (ISO date)
}

// ListSessionsFiltered returns sessions for a project with optional filters and sorting.
func (s *Store) ListSessionsFiltered(projectID int64, f SessionFilter) ([]model.SessionEntry, error) {
	query := `
		SELECT session_id, first_prompt, message_count, created_at, modified_at,
			   git_branch, project_path, todo_file_name, has_file_history,
			   bash_command_count, tool_use_counts, estimated_tokens
		FROM sessions WHERE project_id = ?`
	args := []any{projectID}

	if f.Branch != "" {
		query += ` AND git_branch = ?`
		args = append(args, f.Branch)
	}
	if f.From != "" {
		query += ` AND created_at >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		query += ` AND created_at <= ?`
		args = append(args, f.To+"T23:59:59Z")
	}

	switch f.Sort {
	case "oldest":
		query += ` ORDER BY modified_at ASC`
	case "messages":
		query += ` ORDER BY message_count DESC`
	case "tokens":
		query += ` ORDER BY estimated_tokens DESC`
	case "tools":
		query += ` ORDER BY (SELECT COALESCE(SUM(value), 0) FROM json_each(tool_use_counts)) DESC`
	default:
		query += ` ORDER BY modified_at DESC`
	}

	var rows []struct {
		SessionID        string `db:"session_id"`
		FirstPrompt      string `db:"first_prompt"`
		MessageCount     int    `db:"message_count"`
		CreatedAt        string `db:"created_at"`
		ModifiedAt       string `db:"modified_at"`
		GitBranch        string `db:"git_branch"`
		ProjectPath      string `db:"project_path"`
		TodoFileName     string `db:"todo_file_name"`
		HasFileHistory   int    `db:"has_file_history"`
		BashCommandCount int    `db:"bash_command_count"`
		ToolUseCounts    string `db:"tool_use_counts"`
		EstimatedTokens  int    `db:"estimated_tokens"`
	}
	if err := s.db.Select(&rows, query, args...); err != nil {
		return nil, err
	}
	sessions := make([]model.SessionEntry, len(rows))
	for i, r := range rows {
		toolCounts := make(map[string]int)
		_ = json.Unmarshal([]byte(r.ToolUseCounts), &toolCounts)
		sessions[i] = model.SessionEntry{
			SessionID:        r.SessionID,
			FirstPrompt:      r.FirstPrompt,
			MessageCount:     r.MessageCount,
			Created:          r.CreatedAt,
			Modified:         r.ModifiedAt,
			GitBranch:        r.GitBranch,
			ProjectPath:      r.ProjectPath,
			TodoFileName:     r.TodoFileName,
			HasFileHistory:   r.HasFileHistory != 0,
			BashCommandCount: r.BashCommandCount,
			ToolUseCounts:    toolCounts,
			EstimatedTokens:  r.EstimatedTokens,
		}
	}
	return sessions, nil
}

// GetProjectID returns the internal database ID for a project dir_name.
func (s *Store) GetProjectID(dirName string) (int64, error) {
	var id int64
	err := s.db.Get(&id, `SELECT id FROM projects WHERE dir_name = ?`, dirName)
	return id, err
}

// ListBranches returns distinct git branches for a project.
func (s *Store) ListBranches(projectID int64) ([]string, error) {
	var branches []string
	err := s.db.Select(&branches, `
		SELECT DISTINCT git_branch FROM sessions
		WHERE project_id = ? AND git_branch != ''
		ORDER BY git_branch`, projectID)
	return branches, err
}

// GetSession finds a session by its session_id string, returning the session
// plus its project. Uses a direct JOIN query instead of loading all sessions.
func (s *Store) GetSession(dirName, sessionID string) (*model.ProjectEntry, *model.SessionEntry, error) {
	var row struct {
		// Project fields
		ProjectDirName     string `db:"dir_name"`
		ProjectDisplayName string `db:"display_name"`
		// Session fields
		SessionID        string `db:"session_id"`
		FirstPrompt      string `db:"first_prompt"`
		MessageCount     int    `db:"message_count"`
		CreatedAt        string `db:"created_at"`
		ModifiedAt       string `db:"modified_at"`
		GitBranch        string `db:"git_branch"`
		ProjectPath      string `db:"project_path"`
		TodoFileName     string `db:"todo_file_name"`
		HasFileHistory   int    `db:"has_file_history"`
		BashCommandCount int    `db:"bash_command_count"`
		ToolUseCounts    string `db:"tool_use_counts"`
		EstimatedTokens  int    `db:"estimated_tokens"`
	}
	err := s.db.Get(&row, `
		SELECT p.dir_name, p.display_name,
			   s.session_id, s.first_prompt, s.message_count, s.created_at, s.modified_at,
			   s.git_branch, s.project_path, s.todo_file_name, s.has_file_history,
			   s.bash_command_count, s.tool_use_counts, s.estimated_tokens
		FROM sessions s
		JOIN projects p ON s.project_id = p.id
		WHERE p.dir_name = ? AND s.session_id = ?`, dirName, sessionID)
	if err != nil {
		return nil, nil, err
	}

	toolCounts := make(map[string]int)
	_ = json.Unmarshal([]byte(row.ToolUseCounts), &toolCounts)

	project := &model.ProjectEntry{
		DirName:     row.ProjectDirName,
		DisplayName: row.ProjectDisplayName,
	}
	session := &model.SessionEntry{
		SessionID:        row.SessionID,
		FirstPrompt:      row.FirstPrompt,
		MessageCount:     row.MessageCount,
		Created:          row.CreatedAt,
		Modified:         row.ModifiedAt,
		GitBranch:        row.GitBranch,
		ProjectPath:      row.ProjectPath,
		TodoFileName:     row.TodoFileName,
		HasFileHistory:   row.HasFileHistory != 0,
		BashCommandCount: row.BashCommandCount,
		ToolUseCounts:    toolCounts,
		EstimatedTokens:  row.EstimatedTokens,
	}
	return project, session, nil
}

// GetSessionMessages returns conversation messages for a session, with pagination.
func (s *Store) GetSessionMessages(sessionID string, offset, limit int) ([]model.ConversationMessage, int, error) {
	// Get the database session ID
	var dbID int64
	if err := s.db.Get(&dbID, `SELECT id FROM sessions WHERE session_id = ?`, sessionID); err != nil {
		return nil, 0, err
	}

	// Total count
	var total int
	if err := s.db.Get(&total, `SELECT COUNT(*) FROM messages WHERE session_id = ?`, dbID); err != nil {
		return nil, 0, err
	}

	// Fetch page
	var rows []struct {
		Type      string `db:"type"`
		Role      string `db:"role"`
		Timestamp string `db:"timestamp"`
		UUID      string `db:"uuid"`
		Content   string `db:"content"`
		Cwd       string `db:"cwd"`
		GitBranch string `db:"git_branch"`
	}
	if err := s.db.Select(&rows, `
		SELECT type, role, timestamp, uuid, content, cwd, git_branch
		FROM messages WHERE session_id = ? ORDER BY seq LIMIT ? OFFSET ?`,
		dbID, limit, offset,
	); err != nil {
		return nil, 0, err
	}

	messages := make([]model.ConversationMessage, len(rows))
	for i, r := range rows {
		messages[i] = model.ConversationMessage{
			Type:      r.Type,
			Timestamp: r.Timestamp,
			UUID:      r.UUID,
			Cwd:       r.Cwd,
			GitBranch: r.GitBranch,
			Message: model.MessagePayload{
				Role:    r.Role,
				Content: json.RawMessage(r.Content),
			},
		}
	}
	return messages, total, nil
}

// GetAllSessionMessages returns all conversation messages for a session (no pagination).
func (s *Store) GetAllSessionMessages(sessionID string) ([]model.ConversationMessage, error) {
	var dbID int64
	if err := s.db.Get(&dbID, `SELECT id FROM sessions WHERE session_id = ?`, sessionID); err != nil {
		return nil, err
	}

	var rows []struct {
		Type      string `db:"type"`
		Role      string `db:"role"`
		Timestamp string `db:"timestamp"`
		UUID      string `db:"uuid"`
		Content   string `db:"content"`
		Cwd       string `db:"cwd"`
		GitBranch string `db:"git_branch"`
	}
	if err := s.db.Select(&rows, `
		SELECT type, role, timestamp, uuid, content, cwd, git_branch
		FROM messages WHERE session_id = ? ORDER BY seq`, dbID); err != nil {
		return nil, err
	}

	messages := make([]model.ConversationMessage, len(rows))
	for i, r := range rows {
		messages[i] = model.ConversationMessage{
			Type:      r.Type,
			Timestamp: r.Timestamp,
			UUID:      r.UUID,
			Cwd:       r.Cwd,
			GitBranch: r.GitBranch,
			Message: model.MessagePayload{
				Role:    r.Role,
				Content: json.RawMessage(r.Content),
			},
		}
	}
	return messages, nil
}

// ListHistory returns history entries sorted newest first, with a limit.
func (s *Store) ListHistory(limit int) ([]model.HistoryEntry, error) {
	var entries []model.HistoryEntry
	if err := s.db.Select(&entries, `SELECT display, timestamp, project FROM history ORDER BY timestamp DESC LIMIT ?`, limit); err != nil {
		return nil, err
	}
	return entries, nil
}

// ListAllHistory returns all history entries sorted newest first.
func (s *Store) ListAllHistory() ([]model.HistoryEntry, error) {
	var entries []model.HistoryEntry
	if err := s.db.Select(&entries, `SELECT display, timestamp, project FROM history ORDER BY timestamp DESC`); err != nil {
		return nil, err
	}
	return entries, nil
}

// HeatmapDayCounts holds a date and its activity count.
type HeatmapDayCount struct {
	Date  string `db:"day"`
	Count int    `db:"cnt"`
}

// HistoryDayCounts returns per-day activity counts from history, computed in SQL.
func (s *Store) HistoryDayCounts() (map[string]int, error) {
	var rows []HeatmapDayCount
	err := s.db.Select(&rows, `
		SELECT DATE(timestamp / 1000, 'unixepoch') AS day, COUNT(*) AS cnt
		FROM history GROUP BY day`)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int, len(rows))
	for _, r := range rows {
		counts[r.Date] = r.Count
	}
	return counts, nil
}

// ListFileHistory returns file history entries sorted by file count desc.
func (s *Store) ListFileHistory() ([]model.FileHistoryEntry, error) {
	var rows []struct {
		ConversationID string         `db:"conversation_id"`
		FileCount      int            `db:"file_count"`
		ProjectDir     sql.NullString `db:"project_dir"`
		ProjectName    sql.NullString `db:"project_name"`
	}
	err := s.db.Select(&rows, `
		SELECT fh.conversation_id, fh.file_count,
			   p.dir_name AS project_dir,
			   p.display_name AS project_name
		FROM file_history fh
		LEFT JOIN sessions s ON fh.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		ORDER BY fh.file_count DESC`)
	if err != nil {
		return nil, err
	}
	entries := make([]model.FileHistoryEntry, len(rows))
	for i, r := range rows {
		entries[i] = model.FileHistoryEntry{
			ConversationID: r.ConversationID,
			FileCount:      r.FileCount,
			ProjectDir:     r.ProjectDir.String,
			ProjectName:    r.ProjectName.String,
		}
	}
	return entries, nil
}

// GetFileHistory returns a file history detail (all versions).
func (s *Store) GetFileHistory(conversationID string) (*model.FileHistoryEntry, *model.FileHistoryDetail, error) {
	var fhRow struct {
		ID             int64          `db:"id"`
		ConversationID string         `db:"conversation_id"`
		FileCount      int            `db:"file_count"`
		ProjectDir     sql.NullString `db:"project_dir"`
		ProjectName    sql.NullString `db:"project_name"`
	}
	err := s.db.Get(&fhRow, `
		SELECT fh.id, fh.conversation_id, fh.file_count,
			   p.dir_name AS project_dir,
			   p.display_name AS project_name
		FROM file_history fh
		LEFT JOIN sessions s ON fh.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		WHERE fh.conversation_id = ?`, conversationID)
	if err != nil {
		return nil, nil, err
	}

	entry := &model.FileHistoryEntry{
		ConversationID: fhRow.ConversationID,
		FileCount:      fhRow.FileCount,
		ProjectDir:     fhRow.ProjectDir.String,
		ProjectName:    fhRow.ProjectName.String,
	}

	var versions []model.FileVersionInfo
	err = s.db.Select(&versions, `
		SELECT hash, version, content FROM file_versions
		WHERE file_history_id = ? ORDER BY hash, version`, fhRow.ID)
	if err != nil {
		return entry, nil, err
	}

	detail := &model.FileHistoryDetail{
		ConversationID: conversationID,
		Files:          versions,
	}
	return entry, detail, nil
}

// SearchResult holds a single FTS search hit.
type SearchResult struct {
	ProjectDirName string
	ProjectDisplay string
	SessionID      string
	SessionPrompt  string
	Role           string
	Timestamp      string
	Snippet        string
}

// SearchFilter holds optional filters for search.
type SearchFilter struct {
	Project string // filter by project dir_name
	Role    string // filter by message role ("user" or "assistant")
	From    string // filter by timestamp >= (ISO date)
	To      string // filter by timestamp <= (ISO date)
}

// Search performs a full-text search across messages with optional filters.
// Snippet markers use safe placeholders that are post-processed by highlightSnippet
// to prevent XSS from user content.
func (s *Store) Search(query string, limit int, f SearchFilter) ([]SearchResult, error) {
	q := `
		SELECT m.role, m.timestamp,
			   s.session_id AS session_id_text, s.first_prompt,
			   p.dir_name, p.display_name,
			   snippet(messages_fts, 0, '[[HL_START]]', '[[HL_END]]', '...', 40) AS snippet
		FROM messages_fts
		JOIN messages m ON messages_fts.rowid = m.id
		JOIN sessions s ON m.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE messages_fts MATCH ?`
	args := []any{query}

	if f.Project != "" {
		q += ` AND p.dir_name = ?`
		args = append(args, f.Project)
	}
	if f.Role != "" {
		q += ` AND m.role = ?`
		args = append(args, f.Role)
	}
	if f.From != "" {
		q += ` AND m.timestamp >= ?`
		args = append(args, f.From)
	}
	if f.To != "" {
		q += ` AND m.timestamp <= ?`
		args = append(args, f.To+"T23:59:59Z")
	}

	q += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	var rows []struct {
		Role           string `db:"role"`
		Timestamp      string `db:"timestamp"`
		SessionID      string `db:"session_id_text"`
		FirstPrompt    string `db:"first_prompt"`
		ProjectDirName string `db:"dir_name"`
		ProjectDisplay string `db:"display_name"`
		Snippet        string `db:"snippet"`
	}
	if err := s.db.Select(&rows, q, args...); err != nil {
		return nil, err
	}
	results := make([]SearchResult, len(rows))
	for i, r := range rows {
		prompt := r.FirstPrompt
		if prompt == "" {
			prompt = r.SessionID
		}
		results[i] = SearchResult{
			ProjectDirName: r.ProjectDirName,
			ProjectDisplay: r.ProjectDisplay,
			SessionID:      r.SessionID,
			SessionPrompt:  prompt,
			Role:           r.Role,
			Timestamp:      r.Timestamp,
			Snippet:        r.Snippet,
		}
	}
	return results, nil
}

// ListProjectNames returns all project dir_name/display_name pairs for filter dropdowns.
func (s *Store) ListProjectNames() ([]struct {
	DirName     string `db:"dir_name"`
	DisplayName string `db:"display_name"`
}, error,
) {
	var rows []struct {
		DirName     string `db:"dir_name"`
		DisplayName string `db:"display_name"`
	}
	err := s.db.Select(&rows, `SELECT dir_name, display_name FROM projects ORDER BY display_name`)
	return rows, err
}

// ToolUsageStat holds an aggregate tool usage count across all sessions.
type ToolUsageStat struct {
	Name    string
	Count   int
	Percent float64
}

// GetToolUsageStats aggregates tool_use_counts across all sessions and returns
// the top tools sorted by usage count.
func (s *Store) GetToolUsageStats(limit int) ([]ToolUsageStat, error) {
	var rows []struct {
		ToolUseCounts string `db:"tool_use_counts"`
	}
	if err := s.db.Select(&rows, `SELECT tool_use_counts FROM sessions WHERE tool_use_counts != '{}'`); err != nil {
		return nil, err
	}

	totals := make(map[string]int)
	for _, r := range rows {
		var counts map[string]int
		if json.Unmarshal([]byte(r.ToolUseCounts), &counts) == nil {
			for name, count := range counts {
				totals[name] += count
			}
		}
	}

	stats := make([]ToolUsageStat, 0, len(totals))
	for name, count := range totals {
		stats = append(stats, ToolUsageStat{Name: name, Count: count})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Count > stats[j].Count
	})
	if limit > 0 && len(stats) > limit {
		stats = stats[:limit]
	}

	// Compute percentages relative to the top tool
	if len(stats) > 0 {
		maxCount := stats[0].Count
		for i := range stats {
			stats[i].Percent = float64(stats[i].Count) / float64(maxCount) * 100
		}
	}

	return stats, nil
}

// ProjectStats holds aggregate stats for a single project.
type ProjectStats struct {
	SessionCount  int    `db:"session_count"`
	TotalMessages int    `db:"total_messages"`
	TotalTokens   int    `db:"total_tokens"`
	FirstSession  string `db:"first_session"`
	LastSession   string `db:"last_session"`
}

// GetProjectStats returns aggregate stats for a project.
func (s *Store) GetProjectStats(projectID int64) (ProjectStats, error) {
	var st ProjectStats
	err := s.db.Get(&st, `
		SELECT
			COUNT(*) AS session_count,
			COALESCE(SUM(message_count), 0) AS total_messages,
			COALESCE(SUM(estimated_tokens), 0) AS total_tokens,
			COALESCE(MIN(created_at), '') AS first_session,
			COALESCE(MAX(modified_at), '') AS last_session
		FROM sessions WHERE project_id = ?`, projectID)
	return st, err
}

// TokenTimelineEntry holds a daily token usage total.
type TokenTimelineEntry struct {
	Date   string `db:"day" json:"date"`
	Tokens int    `db:"tokens" json:"tokens"`
}

// GetTokenTimeline returns daily token totals from session created_at dates.
func (s *Store) GetTokenTimeline() ([]TokenTimelineEntry, error) {
	var entries []TokenTimelineEntry
	err := s.db.Select(&entries, `
		SELECT DATE(created_at) AS day, SUM(estimated_tokens) AS tokens
		FROM sessions
		WHERE created_at != ''
		GROUP BY day
		ORDER BY day`)
	return entries, err
}

// ToolTimelineEntry holds a tool call with its timestamp for timeline rendering.
type ToolTimelineEntry struct {
	Name      string `json:"name"`
	Timestamp string `json:"timestamp"`
}

// GetToolTimeline returns tool_use blocks with timestamps for a session.
func (s *Store) GetToolTimeline(sessionID string) ([]ToolTimelineEntry, error) {
	var dbID int64
	if err := s.db.Get(&dbID, `SELECT id FROM sessions WHERE session_id = ?`, sessionID); err != nil {
		return nil, err
	}

	var rows []struct {
		Content   string `db:"content"`
		Timestamp string `db:"timestamp"`
	}
	if err := s.db.Select(&rows, `
		SELECT content, timestamp FROM messages
		WHERE session_id = ? AND role = 'assistant'
		ORDER BY seq`, dbID); err != nil {
		return nil, err
	}

	var entries []ToolTimelineEntry
	for _, r := range rows {
		// Parse content blocks to find tool_use entries
		var blocks []struct {
			Type string `json:"type"`
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(r.Content), &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "tool_use" && b.Name != "" {
					entries = append(entries, ToolTimelineEntry{
						Name:      b.Name,
						Timestamp: r.Timestamp,
					})
				}
			}
		}
	}
	return entries, nil
}

// GetSourceFileMtime returns the stored mtime for a source file, or 0 if not found.
func (s *Store) GetSourceFileMtime(path string) (int64, error) {
	var mtimeNs int64
	err := s.db.Get(&mtimeNs, `SELECT mtime_ns FROM source_files WHERE path = ?`, path)
	if err != nil {
		return 0, err
	}
	return mtimeNs, nil
}

// ListTaskGroups returns all task groups with non-zero items.
func (s *Store) ListTaskGroups() ([]model.TaskGroupEntry, error) {
	var rows []struct {
		DirName     string         `db:"dir_name"`
		ItemCount   int            `db:"item_count"`
		Statuses    string         `db:"statuses"`
		SessionID   sql.NullString `db:"session_id_text"`
		ProjectDir  sql.NullString `db:"project_dir"`
		ProjectName sql.NullString `db:"project_name"`
	}
	err := s.db.Select(&rows, `
		SELECT tg.dir_name, tg.item_count, tg.statuses,
			   s.session_id AS session_id_text,
			   p.dir_name AS project_dir,
			   p.display_name AS project_name
		FROM task_groups tg
		LEFT JOIN sessions s ON tg.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		ORDER BY tg.item_count DESC`)
	if err != nil {
		return nil, err
	}
	groups := make([]model.TaskGroupEntry, len(rows))
	for i, r := range rows {
		statuses := make(map[string]int)
		_ = json.Unmarshal([]byte(r.Statuses), &statuses)
		groups[i] = model.TaskGroupEntry{
			DirName:     r.DirName,
			ItemCount:   r.ItemCount,
			Statuses:    statuses,
			SessionID:   r.SessionID.String,
			ProjectDir:  r.ProjectDir.String,
			ProjectName: r.ProjectName.String,
		}
	}
	return groups, nil
}

// GetTaskGroup returns a task group and its items.
func (s *Store) GetTaskGroup(dirName string) (*model.TaskGroupEntry, []model.TaskItem, error) {
	var row struct {
		ID          int64          `db:"id"`
		DirName     string         `db:"dir_name"`
		ItemCount   int            `db:"item_count"`
		Statuses    string         `db:"statuses"`
		SessionID   sql.NullString `db:"session_id_text"`
		ProjectDir  sql.NullString `db:"project_dir"`
		ProjectName sql.NullString `db:"project_name"`
	}
	err := s.db.Get(&row, `
		SELECT tg.id, tg.dir_name, tg.item_count, tg.statuses,
			   s.session_id AS session_id_text,
			   p.dir_name AS project_dir,
			   p.display_name AS project_name
		FROM task_groups tg
		LEFT JOIN sessions s ON tg.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		WHERE tg.dir_name = ?`, dirName)
	if err != nil {
		return nil, nil, err
	}

	statuses := make(map[string]int)
	_ = json.Unmarshal([]byte(row.Statuses), &statuses)
	entry := &model.TaskGroupEntry{
		DirName:     row.DirName,
		ItemCount:   row.ItemCount,
		Statuses:    statuses,
		SessionID:   row.SessionID.String,
		ProjectDir:  row.ProjectDir.String,
		ProjectName: row.ProjectName.String,
	}

	var itemRows []struct {
		ItemID      string `db:"item_id"`
		Subject     string `db:"subject"`
		Description string `db:"description"`
		ActiveForm  string `db:"active_form"`
		Status      string `db:"status"`
		Blocks      string `db:"blocks"`
		BlockedBy   string `db:"blocked_by"`
	}
	err = s.db.Select(&itemRows, `
		SELECT item_id, subject, description, active_form, status, blocks, blocked_by
		FROM task_items WHERE task_group_id = ? ORDER BY seq`, row.ID)
	if err != nil {
		return entry, nil, err
	}

	items := make([]model.TaskItem, len(itemRows))
	for i, r := range itemRows {
		var blocks, blockedBy []string
		_ = json.Unmarshal([]byte(r.Blocks), &blocks)
		_ = json.Unmarshal([]byte(r.BlockedBy), &blockedBy)
		items[i] = model.TaskItem{
			ID:          r.ItemID,
			Subject:     r.Subject,
			Description: r.Description,
			ActiveForm:  r.ActiveForm,
			Status:      r.Status,
			Blocks:      blocks,
			BlockedBy:   blockedBy,
		}
	}
	return entry, items, nil
}

// ListPasteCache returns all paste-cache entries sorted by size desc.
func (s *Store) ListPasteCache() ([]model.PasteCacheEntry, error) {
	var rows []struct {
		FileName  string `db:"file_name"`
		SizeBytes int64  `db:"size_bytes"`
		Content   string `db:"content"`
	}
	if err := s.db.Select(&rows, `SELECT file_name, size_bytes, content FROM paste_cache ORDER BY size_bytes DESC`); err != nil {
		return nil, err
	}
	entries := make([]model.PasteCacheEntry, len(rows))
	for i, r := range rows {
		preview := r.Content
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		entries[i] = model.PasteCacheEntry{
			FileName:  r.FileName,
			SizeBytes: r.SizeBytes,
			Preview:   preview,
		}
	}
	return entries, nil
}

// GetPasteCache returns a paste-cache entry and its full content.
func (s *Store) GetPasteCache(fileNameWithoutExt string) (*model.PasteCacheEntry, string, error) {
	var row struct {
		FileName  string `db:"file_name"`
		SizeBytes int64  `db:"size_bytes"`
		Content   string `db:"content"`
	}
	err := s.db.Get(&row,
		`SELECT file_name, size_bytes, content FROM paste_cache
		 WHERE file_name = ? OR REPLACE(file_name, '.txt', '') = ?`,
		fileNameWithoutExt+".txt", fileNameWithoutExt,
	)
	if err != nil {
		return nil, "", err
	}
	entry := &model.PasteCacheEntry{FileName: row.FileName, SizeBytes: row.SizeBytes}
	return entry, row.Content, nil
}

// ListUsageFacets returns all usage facets.
func (s *Store) ListUsageFacets() ([]model.UsageFacetEntry, error) {
	var rows []struct {
		SessionID      string         `db:"session_id_text"`
		UnderlyingGoal string         `db:"underlying_goal"`
		Outcome        string         `db:"outcome"`
		Helpfulness    string         `db:"helpfulness"`
		SessionType    string         `db:"session_type"`
		PrimarySuccess string         `db:"primary_success"`
		BriefSummary   string         `db:"brief_summary"`
		FrictionDetail string         `db:"friction_detail"`
		GoalCategories string         `db:"goal_categories"`
		Satisfaction   string         `db:"satisfaction"`
		FrictionCounts string         `db:"friction_counts"`
		ProjectDir     sql.NullString `db:"project_dir"`
		ProjectName    sql.NullString `db:"project_name"`
	}
	err := s.db.Select(&rows, `
		SELECT uf.session_id_text, uf.underlying_goal, uf.outcome, uf.helpfulness,
			   uf.session_type, uf.primary_success, uf.brief_summary, uf.friction_detail,
			   uf.goal_categories, uf.satisfaction, uf.friction_counts,
			   p.dir_name AS project_dir, p.display_name AS project_name
		FROM usage_facets uf
		LEFT JOIN sessions s ON uf.db_session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		ORDER BY uf.underlying_goal`)
	if err != nil {
		return nil, err
	}
	entries := make([]model.UsageFacetEntry, len(rows))
	for i, r := range rows {
		goals := make(map[string]int)
		sat := make(map[string]int)
		fric := make(map[string]int)
		_ = json.Unmarshal([]byte(r.GoalCategories), &goals)
		_ = json.Unmarshal([]byte(r.Satisfaction), &sat)
		_ = json.Unmarshal([]byte(r.FrictionCounts), &fric)
		entries[i] = model.UsageFacetEntry{
			SessionID:      r.SessionID,
			UnderlyingGoal: r.UnderlyingGoal,
			Outcome:        r.Outcome,
			Helpfulness:    r.Helpfulness,
			SessionType:    r.SessionType,
			PrimarySuccess: r.PrimarySuccess,
			BriefSummary:   r.BriefSummary,
			FrictionDetail: r.FrictionDetail,
			GoalCategories: goals,
			Satisfaction:   sat,
			FrictionCounts: fric,
			ProjectDir:     r.ProjectDir.String,
			ProjectName:    r.ProjectName.String,
		}
	}
	return entries, nil
}

// GetUsageFacet returns a single usage facet by session ID.
func (s *Store) GetUsageFacet(sessionID string) (*model.UsageFacetEntry, error) {
	var row struct {
		SessionID      string         `db:"session_id_text"`
		UnderlyingGoal string         `db:"underlying_goal"`
		Outcome        string         `db:"outcome"`
		Helpfulness    string         `db:"helpfulness"`
		SessionType    string         `db:"session_type"`
		PrimarySuccess string         `db:"primary_success"`
		BriefSummary   string         `db:"brief_summary"`
		FrictionDetail string         `db:"friction_detail"`
		GoalCategories string         `db:"goal_categories"`
		Satisfaction   string         `db:"satisfaction"`
		FrictionCounts string         `db:"friction_counts"`
		ProjectDir     sql.NullString `db:"project_dir"`
		ProjectName    sql.NullString `db:"project_name"`
	}
	err := s.db.Get(&row, `
		SELECT uf.session_id_text, uf.underlying_goal, uf.outcome, uf.helpfulness,
			   uf.session_type, uf.primary_success, uf.brief_summary, uf.friction_detail,
			   uf.goal_categories, uf.satisfaction, uf.friction_counts,
			   p.dir_name AS project_dir, p.display_name AS project_name
		FROM usage_facets uf
		LEFT JOIN sessions s ON uf.db_session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		WHERE uf.session_id_text = ?`, sessionID)
	if err != nil {
		return nil, err
	}
	goals := make(map[string]int)
	sat := make(map[string]int)
	fric := make(map[string]int)
	_ = json.Unmarshal([]byte(row.GoalCategories), &goals)
	_ = json.Unmarshal([]byte(row.Satisfaction), &sat)
	_ = json.Unmarshal([]byte(row.FrictionCounts), &fric)
	return &model.UsageFacetEntry{
		SessionID:      row.SessionID,
		UnderlyingGoal: row.UnderlyingGoal,
		Outcome:        row.Outcome,
		Helpfulness:    row.Helpfulness,
		SessionType:    row.SessionType,
		PrimarySuccess: row.PrimarySuccess,
		BriefSummary:   row.BriefSummary,
		FrictionDetail: row.FrictionDetail,
		GoalCategories: goals,
		Satisfaction:   sat,
		FrictionCounts: fric,
		ProjectDir:     row.ProjectDir.String,
		ProjectName:    row.ProjectName.String,
	}, nil
}

// GetUsageReport returns the stored usage report HTML content.
func (s *Store) GetUsageReport() (string, error) {
	var content string
	err := s.db.Get(&content, `SELECT content FROM usage_report LIMIT 1`)
	if err != nil {
		return "", err
	}
	return content, nil
}

// GetSessionDBID returns the internal database ID for a session_id string.
// It accepts an optional transaction; if tx is non-nil it queries within that tx.
func (s *Store) GetSessionDBID(tx *sqlx.Tx, sessionID string) (int64, error) {
	var id int64
	var err error
	if tx != nil {
		err = tx.Get(&id, `SELECT id FROM sessions WHERE session_id = ?`, sessionID)
	} else {
		err = s.db.Get(&id, `SELECT id FROM sessions WHERE session_id = ?`, sessionID)
	}
	return id, err
}
