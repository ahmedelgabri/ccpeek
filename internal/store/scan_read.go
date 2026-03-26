package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

// ListScanFindings returns all scan findings, optionally filtered.
// Joins with sessions/projects to populate linking fields for message and command findings.
func (s *Store) ListScanFindings(ctx context.Context, ruleFilter, typeFilter string, showIgnored bool) ([]model.ScanFinding, error) {
	var where []string
	var args []any

	if !showIgnored {
		where = append(where, "f.ignored = 0")
	}
	if ruleFilter != "" {
		where = append(where, "f.rule_id = ?")
		args = append(args, ruleFilter)
	}
	if typeFilter != "" {
		where = append(where, "f.source_type = ?")
		args = append(args, typeFilter)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + strings.Join(where, " AND ")
	}

	query := `SELECT f.id, f.rule_id, f.description, f.source_type, f.source_id,
		f.match_redacted, f.line_number, f.scanned_at, f.ignored,
		COALESCE(p.dir_name, '') AS project_dir,
		COALESCE(p.display_name, '') AS project_name,
		COALESCE(s.session_id, '') AS session_id_text
		FROM scan_findings f
		LEFT JOIN sessions s ON (
			(f.source_type IN ('message', 'command') AND s.session_id = CASE
				WHEN INSTR(f.source_id, '@') > 0 THEN SUBSTR(f.source_id, 1, INSTR(f.source_id, '@') - 1)
				ELSE f.source_id
			END)
		)
		LEFT JOIN projects p ON s.project_id = p.id` +
		whereClause + ` ORDER BY f.ignored ASC, f.rule_id, f.id`

	var rows []model.ScanFinding
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	return rows, nil
}

// GetScanStats returns aggregate counts for scan findings (excluding ignored).
func (s *Store) GetScanStats(ctx context.Context) (model.ScanStats, error) {
	stats := model.ScanStats{
		FindingsByRule: make(map[string]int),
		FindingsByType: make(map[string]int),
	}

	if err := s.db.GetContext(ctx, &stats.TotalFindings, `SELECT COUNT(*) FROM scan_findings WHERE ignored = 0`); err != nil {
		return stats, err
	}

	var ruleRows []struct {
		RuleID string `db:"rule_id"`
		Count  int    `db:"cnt"`
	}
	if err := s.db.SelectContext(ctx, &ruleRows, `SELECT rule_id, COUNT(*) AS cnt FROM scan_findings WHERE ignored = 0 GROUP BY rule_id ORDER BY cnt DESC`); err != nil {
		return stats, err
	}
	for _, r := range ruleRows {
		stats.FindingsByRule[r.RuleID] = r.Count
	}

	var typeRows []struct {
		SourceType string `db:"source_type"`
		Count      int    `db:"cnt"`
	}
	if err := s.db.SelectContext(ctx, &typeRows, `SELECT source_type, COUNT(*) AS cnt FROM scan_findings WHERE ignored = 0 GROUP BY source_type ORDER BY cnt DESC`); err != nil {
		return stats, err
	}
	for _, r := range typeRows {
		stats.FindingsByType[r.SourceType] = r.Count
	}

	return stats, nil
}

// ScanFindingCount returns the total number of non-ignored scan findings.
func (s *Store) ScanFindingCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM scan_findings WHERE ignored = 0`)
	return count, err
}

// GetScanFinding returns a single scan finding by ID.
func (s *Store) GetScanFinding(ctx context.Context, id int64) (*model.ScanFinding, error) {
	var f model.ScanFinding
	err := s.db.GetContext(ctx, &f, `
		SELECT f.id, f.rule_id, f.description, f.source_type, f.source_id,
			   f.match_redacted, f.line_number, f.scanned_at, f.ignored,
			   COALESCE(p.dir_name, '') AS project_dir,
			   COALESCE(p.display_name, '') AS project_name,
			   COALESCE(s.session_id, '') AS session_id_text
		FROM scan_findings f
		LEFT JOIN sessions s ON f.source_type IN ('message','command') AND s.session_id = SUBSTR(f.source_id, 1, INSTR(f.source_id || '@', '@') - 1)
		LEFT JOIN projects p ON s.project_id = p.id
		WHERE f.id = ?`, id)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// ToggleScanFindingIgnored toggles the ignored state of a scan finding.
func (s *Store) ToggleScanFindingIgnored(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scan_findings SET ignored = CASE WHEN ignored = 0 THEN 1 ELSE 0 END WHERE id = ?`, id)
	return err
}

// ScanMessageRow holds message data for secret scanning.
type ScanMessageRow struct {
	ID        int64  `db:"id"`
	SessionID string `db:"session_id"`
	Timestamp string `db:"timestamp"`
	Content   string `db:"content"`
	Role      string `db:"role"`
}

// EachMessageForScan iterates over all messages for scanning without
// loading them all into memory at once.
func (s *Store) EachMessageForScan(ctx context.Context, fn func(ScanMessageRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT m.id, s.session_id, m.timestamp, m.content, m.role
		FROM messages m
		JOIN sessions s ON m.session_id = s.id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanMessageRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanCommandRow holds command data for secret scanning.
type ScanCommandRow struct {
	ID        int64  `db:"id"`
	SessionID string `db:"session_id"`
	Timestamp string `db:"timestamp"`
	Command   string `db:"command"`
}

// EachCommandForScan iterates over all commands for scanning.
func (s *Store) EachCommandForScan(ctx context.Context, fn func(ScanCommandRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT tc.id, s.session_id, tc.timestamp,
		       COALESCE(json_extract(tc.input_json, '$.command'), '') AS command
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		WHERE tc.tool_kind = 'shell'
	`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanCommandRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanContentRow holds a file_name + content pair for scanning.
type ScanContentRow struct {
	Name    string `db:"file_name"`
	Content string `db:"content"`
}

// EachContentForScan iterates over named content rows from a table.
func (s *Store) EachContentForScan(ctx context.Context, table string, fn func(ScanContentRow) error) error {
	if !allowedTables[table] {
		return fmt.Errorf("disallowed table name: %s", table)
	}
	rows, err := s.db.QueryxContext(ctx, fmt.Sprintf(`SELECT file_name, content FROM %s`, table))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanContentRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanMemoryRow holds a memory entry for scanning.
type ScanMemoryRow struct {
	ProjectDir string `db:"project_dir"`
	Content    string `db:"content"`
}

// EachMemoryForScan iterates over all memories for scanning.
func (s *Store) EachMemoryForScan(ctx context.Context, fn func(ScanMemoryRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `SELECT project_dir, content FROM memories`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanMemoryRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanTodoRow holds a todo item for scanning.
type ScanTodoRow struct {
	FileName string `db:"file_name"`
	Seq      int    `db:"seq"`
	Content  string `db:"content"`
}

// EachTodoForScan iterates over all todo items for scanning.
func (s *Store) EachTodoForScan(ctx context.Context, fn func(ScanTodoRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT t.file_name, ti.seq, ti.content
		FROM todo_items ti
		JOIN todos t ON ti.todo_id = t.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanTodoRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanTaskRow holds a task item for scanning.
type ScanTaskRow struct {
	DirName     string `db:"dir_name"`
	ItemID      string `db:"item_id"`
	Subject     string `db:"subject"`
	Description string `db:"description"`
}

// EachTaskForScan iterates over all task items for scanning.
func (s *Store) EachTaskForScan(ctx context.Context, fn func(ScanTaskRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT tg.dir_name, ti.item_id, ti.subject, ti.description
		FROM task_items ti
		JOIN task_groups tg ON ti.task_group_id = tg.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanTaskRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanFileVersionRow holds a file-history version for scanning.
type ScanFileVersionRow struct {
	ConversationID string `db:"conversation_id"`
	Content        string `db:"content"`
}

// EachFileVersionForScan iterates over all file-history versions for scanning.
func (s *Store) EachFileVersionForScan(ctx context.Context, fn func(ScanFileVersionRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT fh.conversation_id, fv.content
		FROM file_versions fv
		JOIN file_history fh ON fv.file_history_id = fh.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanFileVersionRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanUsageFacetRow holds a usage facet for scanning.
type ScanUsageFacetRow struct {
	SessionID      string `db:"session_id_text"`
	UnderlyingGoal string `db:"underlying_goal"`
	Outcome        string `db:"outcome"`
	Helpfulness    string `db:"helpfulness"`
	SessionType    string `db:"session_type"`
	PrimarySuccess string `db:"primary_success"`
	BriefSummary   string `db:"brief_summary"`
	FrictionDetail string `db:"friction_detail"`
}

// EachUsageFacetForScan iterates over all usage facets for scanning.
func (s *Store) EachUsageFacetForScan(ctx context.Context, fn func(ScanUsageFacetRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `
		SELECT session_id_text, underlying_goal, outcome, helpfulness,
		       session_type, primary_success, brief_summary, friction_detail
		FROM usage_facets`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanUsageFacetRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ScanUsageReportRow holds the usage report HTML content for scanning.
type ScanUsageReportRow struct {
	Content string `db:"content"`
}

// EachUsageReportForScan iterates over the usage report for scanning.
func (s *Store) EachUsageReportForScan(ctx context.Context, fn func(ScanUsageReportRow) error) error {
	rows, err := s.db.QueryxContext(ctx, `SELECT content FROM usage_report`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r ScanUsageReportRow
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}
