package store

import (
	"context"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

func commandBaseFrom() string {
	return `
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		JOIN projects p ON s.project_id = p.id`
}

func commandWhereClause(filter CommandFilter) (string, []any) {
	var where []string
	var args []any
	where = append(where, "tc.tool_kind = 'shell'")

	if filter.Project != "" {
		where = append(where, "p.dir_name = ?")
		args = append(args, filter.Project)
	}
	if filter.Search != "" {
		where = append(where, `json_extract(tc.input_json, '$.command') LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(filter.Search)+"%")
	}
	if filter.From != "" {
		where = append(where, "tc.timestamp >= ?")
		args = append(args, filter.From)
	}
	if filter.To != "" {
		where = append(where, "tc.timestamp <= ?")
		args = append(args, filter.To+"T23:59:59Z")
	}

	return " WHERE " + strings.Join(where, " AND "), args
}

// EachCommand iterates all matching commands without loading them all into memory.
func (s *Store) EachCommand(ctx context.Context, filter CommandFilter, fn func(model.CommandEntry) error) error {
	baseFrom := commandBaseFrom()
	whereClause, args := commandWhereClause(filter)
	query := `SELECT
			COALESCE(json_extract(tc.input_json, '$.command'), '') AS command,
			tc.timestamp,
			s.session_id,
			s.first_prompt,
			p.dir_name,
			p.display_name` + baseFrom + whereClause +
		" ORDER BY tc.timestamp DESC, tc.seq DESC"

	rows, err := s.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var entry model.CommandEntry
		if err := rows.StructScan(&entry); err != nil {
			return err
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
	return rows.Err()
}
