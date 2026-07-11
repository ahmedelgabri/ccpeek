package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// IngestRun mirrors an ingest_runs row for the diagnostics surfaces
// (`ccpeek ingest`).
type IngestRun struct {
	ID              int64           `json:"id"`
	Mode            string          `json:"mode"`
	Status          string          `json:"status"`
	Roots           json.RawMessage `json:"roots"`
	StartedAt       string          `json:"startedAt"`
	FinishedAt      string          `json:"finishedAt,omitempty"`
	DurationMS      int64           `json:"durationMs"`
	FilesSeen       int             `json:"filesSeen"`
	FilesChanged    int             `json:"filesChanged"`
	RecordsIndexed  int             `json:"recordsIndexed"`
	SkippedRows     int             `json:"skippedRows"`
	ParseFailures   int             `json:"parseFailures"`
	UnresolvedLinks int             `json:"unresolvedLinks"`
	WarningCount    int             `json:"warningCount"`
	ErrorMessage    string          `json:"errorMessage,omitempty"`
}

// IngestIssue mirrors an ingest_issues row.
type IngestIssue struct {
	ID         int64  `json:"id"`
	RunID      int64  `json:"runId"`
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	AgentSlug  string `json:"agent,omitempty"`
	SourcePath string `json:"sourcePath,omitempty"`
	Line       int    `json:"line,omitempty"`
	Detail     string `json:"detail"`
	CreatedAt  string `json:"createdAt"`
}

const ingestRunColumns = `
	id, mode, status, roots_json, started_at, COALESCE(finished_at, ''),
	duration_ms, files_seen, files_changed, records_indexed,
	skipped_rows, parse_failures, unresolved_links, warning_count,
	error_message`

func scanIngestRun(row interface{ Scan(...any) error }) (*IngestRun, error) {
	var r IngestRun
	var roots string
	err := row.Scan(&r.ID, &r.Mode, &r.Status, &roots, &r.StartedAt,
		&r.FinishedAt, &r.DurationMS, &r.FilesSeen, &r.FilesChanged,
		&r.RecordsIndexed, &r.SkippedRows, &r.ParseFailures,
		&r.UnresolvedLinks, &r.WarningCount, &r.ErrorMessage)
	if err != nil {
		return nil, err
	}
	r.Roots = json.RawMessage(roots)
	return &r, nil
}

// ListRuns returns the most recent ingest runs, newest first.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]IngestRun, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+ingestRunColumns+`
		FROM ingest_runs ORDER BY started_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing ingest runs: %w", err)
	}
	defer rows.Close()
	var out []IngestRun
	for rows.Next() {
		r, err := scanIngestRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetRun returns one ingest run by id — the latest when id is 0 — or nil
// when no run matches.
func (s *Store) GetRun(ctx context.Context, id int64) (*IngestRun, error) {
	q := `SELECT ` + ingestRunColumns + ` FROM ingest_runs `
	var row *sql.Row
	if id == 0 {
		row = s.db.QueryRowContext(ctx, q+`ORDER BY started_at DESC, id DESC LIMIT 1`)
	} else {
		row = s.db.QueryRowContext(ctx, q+`WHERE id = ?`, id)
	}
	r, err := scanIngestRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading ingest run: %w", err)
	}
	return r, nil
}

// ListRunIssues returns all diagnostics recorded for a run.
func (s *Store) ListRunIssues(ctx context.Context, runID int64) ([]IngestIssue, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, severity, category, agent_slug, source_path,
		       line_number, detail, created_at
		FROM ingest_issues WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing ingest issues: %w", err)
	}
	defer rows.Close()
	var out []IngestIssue
	for rows.Next() {
		var is IngestIssue
		if err := rows.Scan(&is.ID, &is.RunID, &is.Severity, &is.Category,
			&is.AgentSlug, &is.SourcePath, &is.Line, &is.Detail,
			&is.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, is)
	}
	return out, rows.Err()
}

// SourceSig is the stored change-detection state for one source path.
type SourceSig struct {
	ContentHash string
	StatSig     string
}

// SourceSigs returns the stored signatures for every known source path.
func (s *Store) SourceSigs(ctx context.Context) (map[string]SourceSig, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, content_hash, stat_sig FROM source_files`)
	if err != nil {
		return nil, fmt.Errorf("reading source signatures: %w", err)
	}
	defer rows.Close()
	out := make(map[string]SourceSig)
	for rows.Next() {
		var path string
		var sig SourceSig
		if err := rows.Scan(&path, &sig.ContentHash, &sig.StatSig); err != nil {
			return nil, err
		}
		out[path] = sig
	}
	return out, rows.Err()
}

// TouchSourceStat refreshes only the stat signature of a known source —
// the content proved unchanged after a stat mismatch (e.g. a tool rewrote
// the file with identical bytes), so re-parsing was skipped.
func (s *Store) TouchSourceStat(ctx context.Context, path, statSig string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE source_files SET stat_sig = ? WHERE path = ?`, statSig, path)
	return err
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
