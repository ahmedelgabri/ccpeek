// Package secrets scans v2-indexed content for leaked credentials with
// gitleaks' default ruleset — across EVERY agent's transcripts and
// artifacts, not just Claude's (docs/v2-plan.md §6: nothing else scans
// your Codex/Cursor history for keys).
//
// Findings are derived data (scan_findings); the user's ignore decisions
// live in user_annotations under natural keys shaped exactly like the
// v1 importer writes them — "<entity_type>/<source_id>/<rule_id>/<line>" —
// so flags imported from v1 re-attach to re-detected findings.
package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/zricethezav/gitleaks/v8/detect"

	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// Finding is one detected secret.
type Finding struct {
	RuleID        string `json:"ruleId"`
	Description   string `json:"description"`
	EntityType    string `json:"entityType"` // message | artifact
	NaturalKey    string `json:"naturalKey"`
	MatchRedacted string `json:"matchRedacted"`
	Line          int    `json:"line"`
	Ignored       bool   `json:"ignored"`
}

// Scanner runs gitleaks over the v2 store.
type Scanner struct {
	detector *detect.Detector
	store    *db.Store
}

// New builds a Scanner with the default gitleaks ruleset.
func New(store *db.Store) (*Scanner, error) {
	d, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("initializing secret detector: %w", err)
	}
	return &Scanner{detector: d, store: store}, nil
}

// Run scans all indexed content and replaces scan_findings. It returns
// every finding with its ignored state resolved from user_annotations.
func (sc *Scanner) Run(ctx context.Context) ([]Finding, error) {
	var all []Finding

	if err := sc.scanMessages(ctx, &all); err != nil {
		return nil, err
	}
	if err := sc.scanArtifacts(ctx, &all); err != nil {
		return nil, err
	}

	sqlDB := sc.store.DB()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_findings`); err != nil {
		return nil, fmt.Errorf("clearing findings: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO scan_findings
			(rule_id, description, entity_type, natural_key, match_redacted, line_number, scanned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, f := range all {
		if _, err := stmt.ExecContext(ctx, f.RuleID, f.Description,
			f.EntityType, f.NaturalKey, f.MatchRedacted, f.Line, now); err != nil {
			return nil, fmt.Errorf("inserting finding: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Resolve ignore flags (user state survives rescans and rebuilds).
	ignored := map[string]bool{}
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT natural_key FROM user_annotations
		WHERE entity_type = 'scan_finding' AND kind = 'scan_ignore'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		ignored[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range all {
		all[i].Ignored = ignored[annotationKey(all[i])]
	}
	return all, nil
}

// annotationKey matches the v1 importer's shape:
// "<entity_type>/<source_id>/<rule_id>/<line>".
func annotationKey(f Finding) string {
	return fmt.Sprintf("%s/%s/%d", f.NaturalKey, f.RuleID, f.Line)
}

func (sc *Scanner) scanMessages(ctx context.Context, out *[]Finding) error {
	rows, err := sc.store.DB().QueryContext(ctx, `
		SELECT a.slug, s.external_id, m.seq, m.content
		FROM messages m
		JOIN sessions s ON s.id = m.session_id
		JOIN agents a ON a.id = s.agent_id
		ORDER BY s.id, m.seq`)
	if err != nil {
		return fmt.Errorf("reading messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var slug, sessionID, content string
		var seq int
		if err := rows.Scan(&slug, &sessionID, &seq, &content); err != nil {
			return err
		}
		for _, g := range sc.detector.DetectString(content) {
			*out = append(*out, Finding{
				RuleID:      g.RuleID,
				Description: g.Description,
				EntityType:  "message",
				// v1-compatible source id: the session external id. Seq
				// rides in the line slot's sibling field below.
				NaturalKey:    "message/" + sessionID,
				MatchRedacted: redact(g.Secret),
				Line:          seq,
			})
		}
	}
	return rows.Err()
}

func (sc *Scanner) scanArtifacts(ctx context.Context, out *[]Finding) error {
	rows, err := sc.store.DB().QueryContext(ctx, `
		SELECT ar.kind, ar.name, ar.content, ar.metadata_json
		FROM artifacts ar ORDER BY ar.id`)
	if err != nil {
		return fmt.Errorf("reading artifacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var kind, name, content, metadata string
		if err := rows.Scan(&kind, &name, &content, &metadata); err != nil {
			return err
		}
		text := content
		if len(metadata) > 2 { // structured children (todos, versions) live here
			text += "\n" + metadata
		}
		for _, g := range sc.detector.DetectString(text) {
			*out = append(*out, Finding{
				RuleID:        g.RuleID,
				Description:   g.Description,
				EntityType:    "artifact",
				NaturalKey:    kind + "/" + name,
				MatchRedacted: redact(g.Secret),
				Line:          g.StartLine,
			})
		}
	}
	return rows.Err()
}

// redact keeps just enough of a secret to recognize it.
func redact(secret string) string {
	if len(secret) <= 8 {
		return "****"
	}
	return secret[:4] + "…" + secret[len(secret)-2:]
}
