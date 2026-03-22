package store

import (
	"context"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

// CommandFilter holds optional filter parameters for listing commands.
type CommandFilter struct {
	Project string // filter by project dir_name
	Search  string // filter by command text (LIKE)
	From    string // filter by timestamp >= (ISO date)
	To      string // filter by timestamp <= (ISO date)
}

// ListCommands returns bash commands across all sessions with optional filters.
func (s *Store) ListCommands(ctx context.Context, limit, offset int, filter CommandFilter) ([]model.CommandEntry, int, error) {
	baseFrom := commandBaseFrom()
	whereClause, args := commandWhereClause(filter)

	var total int
	countArgs := make([]any, len(args))
	copy(countArgs, args)
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*)"+baseFrom+whereClause, countArgs...); err != nil {
		return nil, 0, err
	}

	query := `SELECT
			COALESCE(json_extract(tc.input_json, '$.command'), '') AS command,
			tc.timestamp,
			s.session_id,
			s.first_prompt,
			p.dir_name,
			p.display_name` + baseFrom + whereClause +
		" ORDER BY tc.timestamp DESC, tc.seq DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	var rows []model.CommandEntry
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, 0, err
	}

	return rows, total, nil
}

// ListAllCommands returns all bash commands (no pagination) with optional filters.
// Used for export.
func (s *Store) ListAllCommands(ctx context.Context, filter CommandFilter) ([]model.CommandEntry, error) {
	var rows []model.CommandEntry
	if err := s.EachCommand(ctx, filter, func(entry model.CommandEntry) error {
		rows = append(rows, entry)
		return nil
	}); err != nil {
		return nil, err
	}
	return rows, nil
}

// CommandCount returns the total number of commands in the database.
func (s *Store) CommandCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.GetContext(ctx, &count, `SELECT COUNT(*) FROM tool_calls WHERE tool_kind = 'shell'`)
	return count, err
}
