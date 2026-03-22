package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func (s *Store) RebuildSearchIndex(ctx context.Context, tx *sqlx.Tx) error {
	stmts := []string{
		`DROP TABLE IF EXISTS search_documents_fts`,
		`CREATE VIRTUAL TABLE search_documents_fts USING fts5(
			group_type UNINDEXED,
			title UNINDEXED,
			subtitle UNINDEXED,
			url UNINDEXED,
			text_content
		)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RepopulateSearchIndex(ctx context.Context, tx *sqlx.Tx) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO search_documents_fts (group_type, title, subtitle, url, text_content)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	populate := []func(context.Context, *sqlx.Tx, *sql.Stmt) error{
		s.populateConversationSearchIndex,
		s.populateCommandSearchIndex,
		s.populateMemorySearchIndex,
		s.populatePlanSearchIndex,
		s.populateTodoSearchIndex,
		s.populateTaskSearchIndex,
		s.populatePasteCacheSearchIndex,
		s.populateSnapshotSearchIndex,
		s.populateUsageDataSearchIndex,
	}
	for _, fn := range populate {
		if err := fn(ctx, tx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) backfillSearchIndex(ctx context.Context) error {
	var count int
	if err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM search_documents_fts`); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	tx, err := s.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := s.RebuildSearchIndex(ctx, tx); err != nil {
		return err
	}
	if err := s.RepopulateSearchIndex(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func insertSearchDocument(ctx context.Context, stmt *sql.Stmt, groupType, title, subtitle, url, textContent string) error {
	if strings.TrimSpace(textContent) == "" {
		return nil
	}
	_, err := stmt.ExecContext(ctx, groupType, title, subtitle, url, textContent)
	return err
}

func (s *Store) populateConversationSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `
		SELECT m.role, m.timestamp, m.content,
		       s.session_id AS session_id_text, s.first_prompt,
		       p.dir_name, p.display_name
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		ORDER BY m.id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			Role           string `db:"role"`
			Timestamp      string `db:"timestamp"`
			Content        string `db:"content"`
			SessionID      string `db:"session_id_text"`
			FirstPrompt    string `db:"first_prompt"`
			ProjectDirName string `db:"dir_name"`
			ProjectDisplay string `db:"display_name"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}

		msg := model.MessagePayload{Content: []byte(r.Content)}
		textContent := msg.SearchText()
		if textContent == "" {
			continue
		}

		title := r.FirstPrompt
		if title == "" {
			title = r.SessionID
		}
		url := "/projects/" + r.ProjectDirName + "/" + r.SessionID + "/"
		if r.Timestamp != "" {
			url += anchor("msg", r.Timestamp)
		}
		subtitle := r.ProjectDisplay
		if r.Role != "" {
			if subtitle != "" {
				subtitle += " · " + r.Role
			} else {
				subtitle = r.Role
			}
		}

		if err := insertSearchDocument(ctx, stmt, searchGroupConversations, title, subtitle, url, textContent); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populateCommandSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `
		SELECT COALESCE(json_extract(tc.input_json, '$.command'), '') AS command,
		       tc.timestamp, s.session_id, p.dir_name, p.display_name
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE tc.tool_kind = 'shell'
		ORDER BY tc.timestamp DESC, tc.seq DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			Command        string `db:"command"`
			Timestamp      string `db:"timestamp"`
			SessionID      string `db:"session_id"`
			ProjectDirName string `db:"dir_name"`
			ProjectDisplay string `db:"display_name"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if strings.TrimSpace(r.Command) == "" {
			continue
		}
		url := "/projects/" + r.ProjectDirName + "/" + r.SessionID + "/commands/"
		if r.Timestamp != "" {
			url += anchor("cmd", r.Timestamp)
		}
		if err := insertSearchDocument(ctx, stmt, searchGroupCommands, truncateStr(r.Command, 120), r.ProjectDisplay, url, r.Command); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populateMemorySearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `
		SELECT m.project_dir, m.content,
		       COALESCE(p.display_name, m.project_dir) AS display_name
		FROM memories m
		LEFT JOIN projects p ON m.project_id = p.id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			ProjectDir  string `db:"project_dir"`
			Content     string `db:"content"`
			DisplayName string `db:"display_name"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := insertSearchDocument(ctx, stmt, searchGroupMemories, r.DisplayName, "MEMORY.md", "/memories/"+r.ProjectDir+"/", r.Content); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populatePlanSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `SELECT file_name, title, content FROM plans`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			FileName string `db:"file_name"`
			Title    string `db:"title"`
			Content  string `db:"content"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		textContent := strings.TrimSpace(strings.Join([]string{r.Title, r.Content}, "\n"))
		if err := insertSearchDocument(ctx, stmt, searchGroupPlans, r.Title, r.FileName, "/plans/"+strings.TrimSuffix(r.FileName, ".md")+"/", textContent); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populateTodoSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `
		SELECT ti.content, ti.status, ti.seq, t.file_name,
		       COALESCE(p.display_name, '') AS display_name
		FROM todo_items ti
		JOIN todos t ON ti.todo_id = t.id
		LEFT JOIN sessions s ON t.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			Content     string `db:"content"`
			Status      string `db:"status"`
			Seq         int    `db:"seq"`
			FileName    string `db:"file_name"`
			DisplayName string `db:"display_name"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		subtitle := r.Status
		if r.DisplayName != "" {
			subtitle = r.DisplayName + " · " + r.Status
		}
		url := "/todos/" + strings.TrimSuffix(r.FileName, ".json") + "/#item-" + strconv.Itoa(r.Seq)
		if err := insertSearchDocument(ctx, stmt, searchGroupTodos, truncateStr(r.Content, 100), subtitle, url, r.Content); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populateTaskSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `
		SELECT ti.item_id, ti.subject, ti.description, ti.status, tg.dir_name,
		       COALESCE(p.display_name, '') AS display_name
		FROM task_items ti
		JOIN task_groups tg ON ti.task_group_id = tg.id
		LEFT JOIN sessions s ON tg.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			ItemID      string `db:"item_id"`
			Subject     string `db:"subject"`
			Description string `db:"description"`
			Status      string `db:"status"`
			DirName     string `db:"dir_name"`
			DisplayName string `db:"display_name"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		title := r.Subject
		if title == "" {
			title = r.ItemID
		}
		subtitle := r.Status
		if r.DisplayName != "" {
			subtitle = r.DisplayName + " · " + r.Status
		}
		url := "/tasks/" + r.DirName + "/"
		if r.ItemID != "" {
			url += "#task-" + r.ItemID
		}
		textContent := strings.TrimSpace(strings.Join([]string{r.Subject, r.Description}, "\n"))
		if err := insertSearchDocument(ctx, stmt, searchGroupTasks, title, subtitle, url, textContent); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populatePasteCacheSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `SELECT file_name, content, size_bytes FROM paste_cache`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			FileName  string `db:"file_name"`
			Content   string `db:"content"`
			SizeBytes int64  `db:"size_bytes"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := insertSearchDocument(ctx, stmt, searchGroupPasteCache, r.FileName, formatBytesStore(r.SizeBytes), "/paste-cache/"+strings.TrimSuffix(r.FileName, ".txt")+"/", r.Content); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populateSnapshotSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `SELECT file_name, content FROM shell_snapshots`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			FileName string `db:"file_name"`
			Content  string `db:"content"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := insertSearchDocument(ctx, stmt, searchGroupSnapshots, r.FileName, "Shell snapshot", "/shell-snapshots/"+strings.TrimSuffix(r.FileName, ".sh")+"/", r.Content); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) populateUsageDataSearchIndex(ctx context.Context, tx *sqlx.Tx, stmt *sql.Stmt) error {
	rows, err := tx.QueryxContext(ctx, `
		SELECT session_id_text, brief_summary, underlying_goal, outcome,
		       friction_detail, primary_success
		FROM usage_facets`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var r struct {
			SessionID      string `db:"session_id_text"`
			BriefSummary   string `db:"brief_summary"`
			UnderlyingGoal string `db:"underlying_goal"`
			Outcome        string `db:"outcome"`
			FrictionDetail string `db:"friction_detail"`
			PrimarySuccess string `db:"primary_success"`
		}
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		title := r.BriefSummary
		if title == "" {
			title = r.UnderlyingGoal
		}
		if title == "" {
			title = r.SessionID
		}
		parts := make([]string, 0, 4)
		for _, part := range []string{r.BriefSummary, r.UnderlyingGoal, r.PrimarySuccess, r.FrictionDetail} {
			if strings.TrimSpace(part) != "" {
				parts = append(parts, part)
			}
		}
		if err := insertSearchDocument(ctx, stmt, searchGroupUsageData, truncateStr(title, 80), r.Outcome, "/usage-data/"+r.SessionID+"/", strings.Join(parts, "\n")); err != nil {
			return err
		}
	}
	return rows.Err()
}
