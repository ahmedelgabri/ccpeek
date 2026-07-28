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

// UnpricedTokenSums are the four token columns restricted to the rows the
// agent did NOT report a cost for — the tokens auto mode has to price
// itself. Every surface that computes a cost selects exactly these, in
// this order, and feeds them to AutoCost. The alias `u` is message_usage.
const UnpricedTokenSums = `SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.input_tokens ELSE 0 END),
	SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.output_tokens ELSE 0 END),
	SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_read_tokens ELSE 0 END),
	SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_write_tokens ELSE 0 END)`

// AutoCost prices the tokens an agent did not report a cost for — the
// second half of cost mode "auto" (docs/v2-plan.md §5.3), the first being
// SUM(COALESCE(u.reported_cost_usd, 0)) which needs no pricing table.
//
// It reports the cost, the tokens left UNPRICED because the table could
// not resolve the model, and whether it resolved at all. Surfaces show the
// unpriced count rather than a silent $0; the rollups keep the boolean
// because they count unpriceable GROUPS, not tokens.
//
// This is the product's core arithmetic and it used to be written out at
// each of its three call sites, where the rule could drift by surface: the
// session list, the blocks view, and the usage rollups all read different
// queries over the same corpus, so a divergence would be invisible.
func AutoCost(p Pricer, model string, in, out, cacheRead, cacheWrite int64) (cost float64, unpriced int64, priced bool) {
	rate, ok := p.Lookup(model)
	total := in + out + cacheRead + cacheWrite
	switch {
	case total == 0:
		return 0, 0, ok
	case !ok:
		return 0, total, false
	}
	return rate.Cost(canon.Usage{
		InputTokens: in, OutputTokens: out,
		CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
	}), 0, true
}

// RegenerateRollups rebuilds rollup_usage_daily from message_usage in
// cost mode "auto" (docs/v2-plan.md §5.3): where the agent reported its own
// cost (Pi, legacy Claude costUSD) that figure is used; otherwise cost is
// computed from tokens against the pricing table. Groups whose model the
// table can't price carry priced=0 so the UI/CLI can surface "unpriced
// tokens" instead of a silent $0.
//
// SCOPE, stated plainly because the design notes read otherwise: this is a
// FULL rebuild — both rollup tables are emptied and the whole of
// message_usage is re-aggregated. There is no dirty-domain or dirty-session
// scoping anywhere in this path, and the caller's only economy is skipping
// the call entirely on passes that touched no sessions or messages
// (ingest.Runner.Run). The cost is therefore one four-way join over every
// usage row per data-changing pass, which BenchmarkRegenerateRollups puts
// at ~440ms for 100k rows. Scoping it would mean carrying the dirty
// (day, agent, workspace, model) tuples out of the ingest pass and
// reconciling deletions against rollup_session_days as well; nothing here
// does that today.
func (s *Store) RegenerateRollups(ctx context.Context, pricer Pricer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM rollup_usage_daily`); err != nil {
		return fmt.Errorf("clearing rollups: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rollup_session_days`); err != nil {
		return fmt.Errorf("clearing session days: %w", err)
	}
	// ONE scan produces both tables. Grouping by the rollup dimensions
	// PLUS session_id yields exactly the rollup_session_days rows; the
	// coarser rollup_usage_daily is that same result re-accumulated below
	// by dropping session_id from the key, which also makes the distinct
	// session count free (one group per session).
	//
	// The tradeoff is a prepared INSERT per session-day instead of one
	// set-based INSERT..SELECT, against one fewer scan of this four-way
	// join. Measured on 100k usage rows / 4k sessions / 30 days / 2 models
	// (BenchmarkRegenerateRollups): 442ms merged vs 552ms for two scans —
	// the scan dominates, and the 8k prepared execs inside the open
	// transaction do not. Re-measure before reshaping this.
	rows, err := tx.QueryContext(ctx, `
		SELECT
			substr(COALESCE(m.created_at, s.created_at, ''), 1, 10) AS day,
			s.agent_id,
			COALESCE(sw.workspace_id, 0) AS workspace_id,
			m.model,
			s.id,
			COUNT(*),
			SUM(u.input_tokens),
			SUM(u.output_tokens),
			SUM(u.cache_read_tokens),
			SUM(u.cache_write_tokens),
			SUM(COALESCE(u.reported_cost_usd, 0)),
			`+UnpricedTokenSums+`,
			SUM(CASE WHEN u.reported_cost_usd IS NULL THEN 1 ELSE 0 END)
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions s ON s.id = m.session_id
		LEFT JOIN session_workspaces sw ON sw.session_id = s.id
		GROUP BY day, s.agent_id, workspace_id, m.model, s.id`)
	if err != nil {
		return fmt.Errorf("aggregating usage: %w", err)
	}
	defer rows.Close()

	// key identifies one rollup_usage_daily row; the scan yields one group
	// per (key, session), so folding on key both aggregates the daily row
	// and counts its distinct sessions.
	type key struct {
		day                  string
		agentID, workspaceID int64
		model                string
	}
	type rollupRow struct {
		key
		sessions, messages  int64
		in, out, cr, cw     int64
		reported, estimated float64
		unpriced            int64
	}

	sessionDays, err := tx.PrepareContext(ctx, `
		INSERT INTO rollup_session_days (day, agent_id, workspace_id, model, session_id)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer sessionDays.Close()

	daily := map[key]*rollupRow{}
	var order []key
	for rows.Next() {
		var k key
		var sessionID int64
		var messages, in, out, cr, cw int64
		var reported float64
		var uin, uout, ucr, ucw, unreported int64
		if err := rows.Scan(&k.day, &k.agentID, &k.workspaceID, &k.model,
			&sessionID, &messages, &in, &out, &cr, &cw,
			&reported, &uin, &uout, &ucr, &ucw, &unreported); err != nil {
			return err
		}
		if _, err := sessionDays.ExecContext(ctx,
			k.day, k.agentID, k.workspaceID, k.model, sessionID); err != nil {
			return fmt.Errorf("writing session day: %w", err)
		}

		r := daily[k]
		if r == nil {
			r = &rollupRow{key: k}
			daily[k] = r
			order = append(order, k)
		}
		r.sessions++ // one group per session, so this IS the distinct count
		r.messages += messages
		r.in += in
		r.out += out
		r.cr += cr
		r.cw += cw
		r.reported += reported
		if unreported > 0 {
			// The rollups count unpriceable GROUPS, not tokens, so this is
			// the one caller that reads AutoCost's third return.
			cost, _, priced := AutoCost(pricer, k.model, uin, uout, ucr, ucw)
			r.estimated += cost
			if !priced {
				r.unpriced++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO rollup_usage_daily
			(day, agent_id, workspace_id, model, sessions, messages,
			 input_tokens, output_tokens, cache_read_tokens,
			 cache_write_tokens, cost_usd, cost_reported_usd,
			 cost_estimated_usd, priced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, k := range order {
		r := daily[k]
		if _, err := stmt.ExecContext(ctx, r.day, r.agentID, r.workspaceID,
			r.model, r.sessions, r.messages, r.in, r.out, r.cr, r.cw,
			r.reported+r.estimated, r.reported, r.estimated,
			boolInt(r.unpriced == 0)); err != nil {
			return fmt.Errorf("writing rollup: %w", err)
		}
	}
	return tx.Commit()
}
