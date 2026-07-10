package db

import (
	"context"
	"fmt"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// RunCounts is the telemetry a finished ingest run records.
type RunCounts struct {
	FilesSeen       int
	FilesChanged    int
	RecordsIndexed  int
	SkippedRows     int
	ParseFailures   int
	UnresolvedLinks int
	WarningCount    int
}

// StartRun opens an ingest_runs row and returns its id.
func (s *Store) StartRun(ctx context.Context, mode, rootsJSON string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO ingest_runs (mode, status, roots_json, started_at)
		VALUES (?, 'running', ?, ?)`,
		mode, rootsJSON, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("starting ingest run: %w", err)
	}
	return res.LastInsertId()
}

// FinishRun closes an ingest_runs row with final status and counts.
func (s *Store) FinishRun(ctx context.Context, runID int64, status string, started time.Time, counts RunCounts, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE ingest_runs SET
			status = ?, finished_at = ?, duration_ms = ?,
			files_seen = ?, files_changed = ?, records_indexed = ?,
			skipped_rows = ?, parse_failures = ?, unresolved_links = ?,
			warning_count = ?, error_message = ?
		WHERE id = ?`,
		status, time.Now().UTC().Format(time.RFC3339),
		time.Since(started).Milliseconds(),
		counts.FilesSeen, counts.FilesChanged, counts.RecordsIndexed,
		counts.SkippedRows, counts.ParseFailures, counts.UnresolvedLinks,
		counts.WarningCount, errMsg, runID)
	if err != nil {
		return fmt.Errorf("finishing ingest run %d: %w", runID, err)
	}
	return nil
}

// InsertIssues persists diagnostics for a run.
func (s *Store) InsertIssues(ctx context.Context, runID int64, issues []canon.Issue) error {
	if len(issues) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO ingest_issues
			(run_id, severity, category, agent_slug, source_path, line_number, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, is := range issues {
		if _, err := stmt.ExecContext(ctx,
			runID, string(is.Severity), is.Category, string(is.Agent),
			is.SourcePath, is.Line, is.Detail, now); err != nil {
			return fmt.Errorf("inserting issue: %w", err)
		}
	}
	return tx.Commit()
}

// SourceHashes returns the stored content hash for every known source path.
func (s *Store) SourceHashes(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, content_hash FROM source_files`)
	if err != nil {
		return nil, fmt.Errorf("reading source hashes: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		out[path] = hash
	}
	return out, rows.Err()
}

// RegenerateWorkspaces rebuilds the derived workspace facet from
// sessions.cwd (docs/v2-plan.md §5.2: a grouping facet, never a
// container). Sessions with empty cwd simply have no workspace link.
func (s *Store) RegenerateWorkspaces(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmts := []string{
		`DELETE FROM session_workspaces`,
		`DELETE FROM workspaces`,
		`INSERT INTO workspaces (canonical_path, display_name)
		 SELECT DISTINCT cwd,
			CASE WHEN instr(cwd, '/') > 0
			     THEN replace(cwd, rtrim(cwd, replace(cwd, '/', '')), '')
			     ELSE cwd END
		 FROM sessions WHERE cwd <> ''`,
		`INSERT INTO session_workspaces (session_id, workspace_id)
		 SELECT s.id, w.id FROM sessions s
		 JOIN workspaces w ON w.canonical_path = s.cwd`,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("regenerating workspaces: %w", err)
		}
	}
	return tx.Commit()
}
