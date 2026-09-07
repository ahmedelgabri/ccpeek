package query

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/secrets"
)

type ArchiveStatus struct {
	SchemaVersion    int             `json:"schemaVersion"`
	Generation       int64           `json:"generation"`
	DerivedDirty     bool            `json:"derivedDirty"`
	PendingSessions  int             `json:"pendingSessions"`
	ImportedSessions int             `json:"importedSessions"`
	Scan             ScanCoverage    `json:"scan"`
	LastRun          *IndexRunStatus `json:"lastRun,omitempty"`
}
type ScanCoverage struct {
	CompletedAt             string `json:"completedAt,omitempty"`
	RulesFingerprint        string `json:"rulesFingerprint,omitempty"`
	CurrentRulesFingerprint string `json:"currentRulesFingerprint"`
	Generation              int64  `json:"generation"`
	Pending                 bool   `json:"pending"`
	Scope                   string `json:"scope"`
}
type IndexRunStatus struct {
	ID            int64  `json:"id"`
	Status        string `json:"status"`
	StartedAt     string `json:"startedAt"`
	FinishedAt    string `json:"finishedAt,omitempty"`
	ParseFailures int    `json:"parseFailures"`
	Warnings      int    `json:"warnings"`
	Truncations   int    `json:"truncations"`
}

// ArchiveStatus reports durable maintenance and scan coverage, even when the
// process serving this request did not perform the last index or scan.
func (s *Service) ArchiveStatus(ctx context.Context) (ArchiveStatus, error) {
	var out ArchiveStatus
	tx, err := s.store.ReadDB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT key,value FROM meta WHERE key IN ('schema_version','archive_generation','derived_dirty','scan_completed_at','scan_rules_fingerprint','scan_generation','scan_incomplete')`)
	if err != nil {
		return out, err
	}
	meta := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			rows.Close()
			return out, err
		}
		meta[key] = value
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.SchemaVersion, _ = strconv.Atoi(meta["schema_version"])
	out.Generation, _ = strconv.ParseInt(meta["archive_generation"], 10, 64)
	out.DerivedDirty = meta["derived_dirty"] == "1"
	out.Scan = ScanCoverage{CompletedAt: meta["scan_completed_at"], RulesFingerprint: meta["scan_rules_fingerprint"], CurrentRulesFingerprint: secrets.RulesFingerprint(), Scope: "Stored messages, tool inputs and results, and artifacts only. Unreadable, unsupported, omitted or truncated source content is not covered."}
	out.Scan.Generation, _ = strconv.ParseInt(meta["scan_generation"], 10, 64)
	out.Scan.Pending = out.Scan.CompletedAt == "" || out.Scan.RulesFingerprint != out.Scan.CurrentRulesFingerprint || out.Scan.Generation != out.Generation || meta["scan_incomplete"] == "1"
	if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM dirty_sessions),(SELECT COUNT(*) FROM sessions WHERE origin=?)`, canon.OriginImportedV1).Scan(&out.PendingSessions, &out.ImportedSessions); err != nil {
		return out, err
	}
	var run IndexRunStatus
	err = tx.QueryRowContext(ctx, `SELECT id,status,started_at,COALESCE(finished_at,''),parse_failures,warning_count,(SELECT COUNT(*) FROM ingest_issues WHERE run_id=r.id AND category='size') FROM ingest_runs r ORDER BY id DESC LIMIT 1`).Scan(&run.ID, &run.Status, &run.StartedAt, &run.FinishedAt, &run.ParseFailures, &run.Warnings, &run.Truncations)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.LastRun = &run
	}
	return out, tx.Commit()
}
