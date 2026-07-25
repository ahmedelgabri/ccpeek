package query

import (
	"context"
	"fmt"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// ScanFinding is one row of the `scan` op, with ignore state resolved from
// user_annotations.
type ScanFinding struct {
	ID            int64  `json:"id"`
	RuleID        string `json:"ruleId"`
	Description   string `json:"description"`
	EntityType    string `json:"entityType"`
	NaturalKey    string `json:"naturalKey"`
	MatchRedacted string `json:"matchRedacted"`
	Line          int    `json:"line"`
	ScannedAt     string `json:"scannedAt"`
	Ignored       bool   `json:"ignored"`
}

// ScanFindings lists stored findings; includeIgnored controls whether
// user-dismissed rows appear.
func (s *Service) ScanFindings(ctx context.Context, includeIgnored bool) ([]ScanFinding, error) {
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT f.id, f.rule_id, f.description, f.entity_type, f.natural_key,
		       f.match_redacted, f.line_number, f.scanned_at,
		       `+db.ScanIgnoredSQL("f")+`
		FROM scan_findings f
		ORDER BY f.rule_id, f.natural_key, f.line_number`)
	if err != nil {
		return nil, fmt.Errorf("listing scan findings: %w", err)
	}
	defer rows.Close()
	var out []ScanFinding
	for rows.Next() {
		var f ScanFinding
		var ignored int
		if err := rows.Scan(&f.ID, &f.RuleID, &f.Description, &f.EntityType,
			&f.NaturalKey, &f.MatchRedacted, &f.Line, &f.ScannedAt, &ignored); err != nil {
			return nil, err
		}
		f.Ignored = ignored != 0
		if f.Ignored && !includeIgnored {
			continue
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetScanIgnore records or clears the user's ignore decision for a finding.
// The flag lives in user_annotations (natural key), so it survives rescans
// and rebuilds.
func (s *Service) SetScanIgnore(ctx context.Context, findingID int64, ignored bool) error {
	var naturalKey, ruleID string
	var line int
	err := s.store.DB().QueryRowContext(ctx, `
		SELECT natural_key, rule_id, line_number FROM scan_findings WHERE id = ?`,
		findingID).Scan(&naturalKey, &ruleID, &line)
	if err != nil {
		return fmt.Errorf("%w: scan finding %d", ErrNotFound, findingID)
	}
	key := db.ScanIgnoreKey(naturalKey, ruleID, line)
	if ignored {
		_, err = s.store.DB().ExecContext(ctx, `
			INSERT INTO user_annotations (entity_type, natural_key, kind, value_json, created_at)
			VALUES (?, ?, ?, '{}', ?)
			ON CONFLICT(entity_type, natural_key, kind) DO NOTHING`,
			db.ScanFindingEntity, key, db.ScanIgnoreKind,
			time.Now().UTC().Format(time.RFC3339))
	} else {
		_, err = s.store.DB().ExecContext(ctx, `
			DELETE FROM user_annotations
			WHERE entity_type = ? AND kind = ? AND natural_key = ?`,
			db.ScanFindingEntity, db.ScanIgnoreKind, key)
	}
	return err
}
