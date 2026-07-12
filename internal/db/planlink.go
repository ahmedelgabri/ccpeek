package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// NormalizePlanText canonicalizes plan markdown for matching: the plan
// file on disk and the ExitPlanMode input differ in trailing whitespace
// and final newlines, nothing else (measured 36/37 exact matches on a
// real corpus under this normalization).
func NormalizePlanText(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// ExtractPlanText pulls the plan markdown out of an ExitPlanMode call's
// input JSON; ok=false when the input carries no plan.
func ExtractPlanText(inputJSON string) (string, bool) {
	var in struct {
		Plan string `json:"plan"`
	}
	if err := json.Unmarshal([]byte(inputJSON), &in); err != nil ||
		strings.TrimSpace(in.Plan) == "" {
		return "", false
	}
	return in.Plan, true
}

// LinkPlanArtifacts connects plan artifacts to the sessions whose
// ExitPlanMode call carries the same plan text. Plans land on disk as
// slug-named markdown with no session id anywhere in name or metadata,
// so content is the only provenance. Only unlinked plans are examined,
// making every pass after the first a cheap no-op; a plan whose text
// matches calls in several sessions (re-approved, resumed) links to each.
func (s *Store) LinkPlanArtifacts(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ar.id, ar.agent_id, ar.content FROM artifacts ar
		WHERE ar.kind = 'plan' AND ar.content <> ''
		  AND NOT EXISTS (SELECT 1 FROM artifact_sessions ass WHERE ass.artifact_id = ar.id)`)
	if err != nil {
		return 0, fmt.Errorf("listing unlinked plans: %w", err)
	}
	type plan struct {
		id      int64
		agentID int64
		text    string
	}
	var plans []plan
	for rows.Next() {
		var p plan
		var content string
		if err := rows.Scan(&p.id, &p.agentID, &content); err != nil {
			rows.Close()
			return 0, err
		}
		p.text = NormalizePlanText(content)
		plans = append(plans, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(plans) == 0 {
		return 0, nil
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT se.agent_id, tc.session_id, tc.input_json
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		WHERE tc.name = 'ExitPlanMode'`)
	if err != nil {
		return 0, fmt.Errorf("listing plan calls: %w", err)
	}
	type call struct {
		agentID   int64
		sessionID int64
		text      string
	}
	var calls []call
	for rows.Next() {
		var c call
		var input string
		if err := rows.Scan(&c.agentID, &c.sessionID, &input); err != nil {
			rows.Close()
			return 0, err
		}
		if text, ok := ExtractPlanText(input); ok {
			c.text = NormalizePlanText(text)
			calls = append(calls, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	linked := 0
	for _, p := range plans {
		for _, c := range calls {
			if c.agentID != p.agentID || c.text != p.text {
				continue
			}
			res, err := tx.ExecContext(ctx, `
				INSERT INTO artifact_sessions (artifact_id, session_id, relation, evidence)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(artifact_id, session_id, relation) DO NOTHING`,
				p.id, c.sessionID,
				string(canon.LinkProducedBy), string(canon.EvidenceContentRef))
			if err != nil {
				return 0, fmt.Errorf("linking plan %d: %w", p.id, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				linked++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return linked, nil
}
