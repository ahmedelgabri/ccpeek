package db

import (
	"context"
	"fmt"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// Pricer resolves a model identifier to per-token rates. Satisfied by
// *pricing.Table.
type Pricer interface {
	Lookup(model string) (pricing.Rate, bool)
}

// RegenerateRollups rebuilds rollup_usage_daily from message_usage in
// cost mode "auto" (docs/v2-plan.md §5.3): where the agent reported its own
// cost (Pi, legacy Claude costUSD) that figure is used; otherwise cost is
// computed from tokens against the pricing table. Groups whose model the
// table can't price carry priced=0 so the UI/CLI can surface "unpriced
// tokens" instead of a silent $0.
func (s *Store) RegenerateRollups(ctx context.Context, pricer Pricer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM rollup_usage_daily`); err != nil {
		return fmt.Errorf("clearing rollups: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT
			substr(COALESCE(m.created_at, s.created_at, ''), 1, 10) AS day,
			s.agent_id,
			COALESCE(sw.workspace_id, 0) AS workspace_id,
			m.model,
			COUNT(DISTINCT s.id),
			COUNT(*),
			SUM(u.input_tokens),
			SUM(u.output_tokens),
			SUM(u.cache_read_tokens),
			SUM(u.cache_write_tokens),
			SUM(COALESCE(u.reported_cost_usd, 0)),
			SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.input_tokens ELSE 0 END),
			SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.output_tokens ELSE 0 END),
			SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_read_tokens ELSE 0 END),
			SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_write_tokens ELSE 0 END),
			SUM(CASE WHEN u.reported_cost_usd IS NULL THEN 1 ELSE 0 END)
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions s ON s.id = m.session_id
		LEFT JOIN session_workspaces sw ON sw.session_id = s.id
		GROUP BY day, s.agent_id, workspace_id, m.model`)
	if err != nil {
		return fmt.Errorf("aggregating usage: %w", err)
	}
	defer rows.Close()

	type rollupRow struct {
		day                  string
		agentID, workspaceID int64
		model                string
		sessions, messages   int64
		in, out, cr, cw      int64
		cost                 float64
		priced               bool
	}
	var out []rollupRow
	for rows.Next() {
		var r rollupRow
		var reported float64
		var uin, uout, ucr, ucw, unreported int64
		if err := rows.Scan(&r.day, &r.agentID, &r.workspaceID, &r.model,
			&r.sessions, &r.messages, &r.in, &r.out, &r.cr, &r.cw,
			&reported, &uin, &uout, &ucr, &ucw, &unreported); err != nil {
			return err
		}
		r.cost = reported
		r.priced = true
		if unreported > 0 {
			rate, ok := pricer.Lookup(r.model)
			if ok {
				r.cost += rate.Cost(canon.Usage{
					InputTokens: uin, OutputTokens: uout,
					CacheReadTokens: ucr, CacheWriteTokens: ucw,
				})
			} else {
				r.priced = false
			}
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO rollup_usage_daily
			(day, agent_id, workspace_id, model, sessions, messages,
			 input_tokens, output_tokens, cache_read_tokens,
			 cache_write_tokens, cost_usd, priced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range out {
		if _, err := stmt.ExecContext(ctx, r.day, r.agentID, r.workspaceID,
			r.model, r.sessions, r.messages, r.in, r.out, r.cr, r.cw,
			r.cost, boolInt(r.priced)); err != nil {
			return fmt.Errorf("writing rollup: %w", err)
		}
	}
	return tx.Commit()
}
