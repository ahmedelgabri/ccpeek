package query

import (
	"context"
	"database/sql"
	"errors"
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

// ScanRule is one rule's findings summarized. The scan is read RULE FIRST
// — you decide "do I care about Slack tokens at all" before looking at
// occurrences, and a leaked pattern can appear dozens of times — so the
// grouping, the active/ignored split, and the ranking are the shape of the
// data, not a presentation detail.
//
// They were computed only in the web UI, which left `ccpeek query scan`
// and the MCP tool returning flat rows and every agent re-deriving the
// reading the product actually presents.
type ScanRule struct {
	RuleID      string `json:"ruleId"`
	Description string `json:"description"`
	// Findings counts every stored finding of this rule; Active excludes
	// the ones the user dismissed.
	Findings int `json:"findings"`
	Active   int `json:"active"`
	// Entities is how many distinct sessions/artifacts it was found in.
	Entities int    `json:"entities"`
	LastSeen string `json:"lastSeen"`
}

// ScanRules summarizes findings by rule, most active occurrences first —
// the order they are worth looking at in.
func (s *Service) ScanRules(ctx context.Context) ([]ScanRule, error) {
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT f.rule_id,
		       MAX(f.description),
		       COUNT(*),
		       SUM(CASE WHEN `+db.ScanIgnoredSQL("f")+` THEN 0 ELSE 1 END),
		       COUNT(DISTINCT f.natural_key),
		       MAX(f.scanned_at)
		FROM scan_findings f
		GROUP BY f.rule_id
		ORDER BY 4 DESC, 3 DESC, f.rule_id`)
	if err != nil {
		return nil, fmt.Errorf("summarizing scan findings: %w", err)
	}
	defer rows.Close()
	var out []ScanRule
	for rows.Next() {
		var r ScanRule
		if err := rows.Scan(&r.RuleID, &r.Description, &r.Findings,
			&r.Active, &r.Entities, &r.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
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
//
// Only a genuine no-rows lookup is ErrNotFound. Mapping EVERY failure of
// that lookup to "not found" told the caller its finding id was wrong when
// the truth was a locked database or a canceled request — a 404 for what is
// a 500, the same class SetBudget documents just below.
func (s *Service) SetScanIgnore(ctx context.Context, findingID int64, ignored bool) error {
	var naturalKey, ruleID string
	var line int
	err := s.store.DB().QueryRowContext(ctx, `
		SELECT natural_key, rule_id, line_number FROM scan_findings WHERE id = ?`,
		findingID).Scan(&naturalKey, &ruleID, &line)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: scan finding %d", ErrNotFound, findingID)
	}
	if err != nil {
		return fmt.Errorf("looking up scan finding %d: %w", findingID, err)
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
