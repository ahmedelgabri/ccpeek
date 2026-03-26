package store

import (
	"context"
	"database/sql"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

// SaveIngestRun persists an ingest run and its associated issues atomically.
func (s *Store) SaveIngestRun(ctx context.Context, run *model.IngestRun, issues []model.IngestIssue) error {
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO ingest_runs (
			mode, status, claude_dir, started_at, finished_at, duration_ms,
			files_seen, files_changed, records_indexed, skipped_files, skipped_rows,
			parse_failures, unresolved_links, warning_count, error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.Mode, run.Status, run.ClaudeDir, run.StartedAt, run.FinishedAt, run.DurationMS,
		run.FilesSeen, run.FilesChanged, run.RecordsIndexed, run.SkippedFiles, run.SkippedRows,
		run.ParseFailures, run.UnresolvedLinks, run.WarningCount, run.ErrorMessage,
	)
	if err != nil {
		return err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return err
	}
	run.ID = runID

	if len(issues) > 0 {
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO ingest_issues (
				run_id, severity, category, source_type, source_path, line_number, detail, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()

		for i := range issues {
			issues[i].RunID = runID
			if _, err := stmt.ExecContext(ctx,
				issues[i].RunID,
				issues[i].Severity,
				issues[i].Category,
				issues[i].SourceType,
				issues[i].SourcePath,
				issues[i].LineNumber,
				issues[i].Detail,
				issues[i].CreatedAt,
			); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// GetLatestIngestRun returns the most recent ingest run, if any.
func (s *Store) GetLatestIngestRun(ctx context.Context) (*model.IngestRun, error) {
	var run model.IngestRun
	err := s.db.GetContext(ctx, &run, `
		SELECT id, mode, status, claude_dir, started_at, finished_at, duration_ms,
		       files_seen, files_changed, records_indexed, skipped_files, skipped_rows,
		       parse_failures, unresolved_links, warning_count, error_message
		FROM ingest_runs
		ORDER BY started_at DESC, id DESC
		LIMIT 1`)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// GetIngestRun returns a single ingest run by ID.
func (s *Store) GetIngestRun(ctx context.Context, runID int64) (*model.IngestRun, error) {
	var run model.IngestRun
	err := s.db.GetContext(ctx, &run, `
		SELECT id, mode, status, claude_dir, started_at, finished_at, duration_ms,
		       files_seen, files_changed, records_indexed, skipped_files, skipped_rows,
		       parse_failures, unresolved_links, warning_count, error_message
		FROM ingest_runs
		WHERE id = ?`, runID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// ListIngestRuns returns recent ingest runs in reverse chronological order.
func (s *Store) ListIngestRuns(ctx context.Context, limit int) ([]model.IngestRun, error) {
	if limit <= 0 {
		limit = 10
	}
	var runs []model.IngestRun
	err := s.db.SelectContext(ctx, &runs, `
		SELECT id, mode, status, claude_dir, started_at, finished_at, duration_ms,
		       files_seen, files_changed, records_indexed, skipped_files, skipped_rows,
		       parse_failures, unresolved_links, warning_count, error_message
		FROM ingest_runs
		ORDER BY started_at DESC, id DESC
		LIMIT ?`, limit)
	return runs, err
}

// ListIngestIssues returns the issues captured for a specific ingest run.
func (s *Store) ListIngestIssues(ctx context.Context, runID int64) ([]model.IngestIssue, error) {
	var issues []model.IngestIssue
	err := s.db.SelectContext(ctx, &issues, `
		SELECT id, run_id, severity, category, source_type, source_path, line_number, detail, created_at
		FROM ingest_issues
		WHERE run_id = ?
		ORDER BY id`, runID)
	return issues, err
}
