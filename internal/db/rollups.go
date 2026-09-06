package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// Pricer resolves a model identifier to per-token rates. Satisfied by
// *pricing.Table.
type Pricer interface {
	Lookup(model string) (pricing.Rate, bool)
}

// FingerprintedPricer is implemented by immutable pricing tables. The
// fingerprint covers both source bytes and cost-algorithm semantics.
type FingerprintedPricer interface {
	Fingerprint() string
}

const pricingFingerprintMeta = "pricing_rollup_fingerprint"

// PricerFingerprint returns an empty string for synthetic/test pricers whose
// rollups cannot become stale independently of the process that supplied them.
func PricerFingerprint(p Pricer) string {
	if fp, ok := p.(FingerprintedPricer); ok {
		return fp.Fingerprint()
	}
	return ""
}

// PricingModel preserves provider identity for exact lookup while retaining
// the model as the user-facing grouping value. Lookup normalization can still
// fall back to the bare model when no provider-specific key exists.
func PricingModel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" || strings.Contains(model, "/") {
		return model
	}
	return provider + "/" + model
}

func usageTotal(u canon.Usage) int64 {
	return max(u.InputTokens, 0) + max(u.OutputTokens, 0) +
		max(u.CacheReadTokens, 0) + max(u.CacheWriteTokens, 0)
}

func addUsage(dst *canon.Usage, src canon.Usage) {
	dst.InputTokens += src.InputTokens
	dst.OutputTokens += src.OutputTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.CacheWrite1hTokens += src.CacheWrite1hTokens
}

func dayOf(value string) string {
	if len(value) >= len("2006-01-02") {
		return value[:len("2006-01-02")]
	}
	return value
}

// RollupsNeedRegeneration reports whether materialized cost was built with a
// different pricing snapshot/algorithm. Empty rollups with usage also rebuild,
// preserving the migration self-heal behavior.
func (s *Store) RollupsNeedRegeneration(ctx context.Context, p Pricer) (bool, error) {
	full, err := s.rollupsNeedFullRegeneration(ctx, p)
	if err != nil || full {
		return full, err
	}
	var dirty bool
	err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dirty_sessions)`).Scan(&dirty)
	return dirty, err
}

func (s *Store) rollupsNeedFullRegeneration(ctx context.Context, p Pricer) (bool, error) {
	var rollups, usage, full bool
	var stored string
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM rollup_usage_daily),
		       EXISTS(SELECT 1 FROM message_usage),
		       COALESCE((SELECT value FROM meta WHERE key = 'rollups_full'), '0') = '1',
		       COALESCE((SELECT value FROM meta WHERE key = ?), '')`,
		pricingFingerprintMeta).Scan(&rollups, &usage, &full, &stored); err != nil {
		return false, err
	}
	if full {
		return true, nil
	}
	if !usage {
		// A source prune or manual cleanup can remove the final usage row.
		// Any remaining rollup is then stale data and must be cleared.
		return rollups, nil
	}
	if !rollups {
		return true, nil
	}
	fingerprint := PricerFingerprint(p)
	return fingerprint != "" && fingerprint != stored, nil
}

// RegenerateRollups fully rebuilds both usage materializations. Reported
// non-zero costs win per row; missing and zero-with-usage reports are calculated.
// Missing model or cache-bucket rates remain visible as unpriced.
func (s *Store) RegenerateRollups(ctx context.Context, pricer Pricer) error {
	return s.regenerateRollups(ctx, pricer, true)
}

// RefreshRollups reprices only days touched by changed or deleted sessions.
// A pricing or schema change still requires a complete rebuild.
func (s *Store) RefreshRollups(ctx context.Context, pricer Pricer) error {
	ctx, unlock, err := s.LockMaintenance(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	full, err := s.rollupsNeedFullRegeneration(ctx, pricer)
	if err != nil {
		return err
	}
	return s.regenerateRollups(ctx, pricer, full)
}

func (s *Store) regenerateRollups(ctx context.Context, pricer Pricer, full bool) error {
	ctx, unlock, err := s.LockMaintenance(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	deleteFilter, usageFilter := "", ""
	if !full {
		for _, q := range []string{
			`DROP TABLE IF EXISTS temp.ccpeek_dirty_days`,
			`CREATE TEMP TABLE ccpeek_dirty_days(day TEXT PRIMARY KEY)`,
			`INSERT OR IGNORE INTO ccpeek_dirty_days SELECT day FROM rollup_session_days WHERE session_id IN (SELECT session_id FROM dirty_sessions)`,
			`INSERT OR IGNORE INTO ccpeek_dirty_days SELECT substr(COALESCE(m.created_at,s.created_at,''),1,10) FROM messages m JOIN sessions s ON s.id=m.session_id WHERE s.id IN (SELECT session_id FROM dirty_sessions)`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		deleteFilter = ` WHERE day IN (SELECT day FROM ccpeek_dirty_days)`
		usageFilter = ` WHERE substr(COALESCE(m.created_at,s.created_at,''),1,10) IN (SELECT day FROM ccpeek_dirty_days)`
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rollup_usage_daily`+deleteFilter); err != nil {
		return fmt.Errorf("clearing rollups: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rollup_session_days`+deleteFilter); err != nil {
		return fmt.Errorf("clearing session days: %w", err)
	}

	// Pricing is request-scoped: a SQL aggregate could cross a long-context
	// threshold or effective-date boundary. Scan canonical rows and fold exact
	// automatic reported-first amounts into the daily materialization.
	rows, err := tx.QueryContext(ctx, `
		SELECT
			COALESCE(m.created_at, s.created_at, '') AS occurred_at,
			s.agent_id,
			COALESCE(sw.workspace_id, 0) AS workspace_id,
			m.provider, m.model, s.id,
			u.input_tokens, u.output_tokens, u.cache_read_tokens,
			u.cache_write_tokens, u.cache_write_1h_tokens,
			`+ReportedCostNanosExpr+`
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions s ON s.id = m.session_id
		LEFT JOIN session_workspaces sw ON sw.session_id = s.id`+usageFilter+`
		ORDER BY m.id`)
	if err != nil {
		return fmt.Errorf("reading usage for rollups: %w", err)
	}
	defer rows.Close()

	type key struct {
		day                  string
		agentID, workspaceID int64
		model                string
	}
	type rollupRow struct {
		key
		sessions, messages int64
		in, out, cr, cw    int64
		cost               pricing.Amount
		reported           pricing.Amount
		estimated          pricing.Amount
		unpriced           canon.Usage
		seenSessions       map[int64]bool
	}

	sessionDays, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO rollup_session_days
			(day, agent_id, workspace_id, model, session_id)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer sessionDays.Close()

	daily := map[key]*rollupRow{}
	var order []key
	for rows.Next() {
		var occurredAt, provider string
		var k key
		var sessionID int64
		var usage canon.Usage
		var reported sql.NullInt64
		if err := rows.Scan(&occurredAt, &k.agentID, &k.workspaceID,
			&provider, &k.model, &sessionID, &usage.InputTokens,
			&usage.OutputTokens, &usage.CacheReadTokens, &usage.CacheWriteTokens,
			&usage.CacheWrite1hTokens, &reported); err != nil {
			return err
		}
		k.day = dayOf(occurredAt)
		if _, err := sessionDays.ExecContext(ctx,
			k.day, k.agentID, k.workspaceID, k.model, sessionID); err != nil {
			return fmt.Errorf("writing session day: %w", err)
		}

		r := daily[k]
		if r == nil {
			r = &rollupRow{key: k, seenSessions: map[int64]bool{}}
			daily[k] = r
			order = append(order, k)
		}
		if !r.seenSessions[sessionID] {
			r.seenSessions[sessionID] = true
			r.sessions++
		}
		r.messages++
		r.in += usage.InputTokens
		r.out += usage.OutputTokens
		r.cr += usage.CacheReadTokens
		r.cw += usage.CacheWriteTokens

		var reportedAmount *pricing.Amount
		if reported.Valid {
			amount := pricing.Amount(reported.Int64)
			reportedAmount = &amount
		}
		at := ParseCostTime(occurredAt)
		cost, err := EvaluateCostAt(pricer, provider, k.model, at, usage, reportedAmount)
		if err != nil {
			return fmt.Errorf("pricing usage: %w", err)
		}
		if r.cost, err = r.cost.Add(cost.Amount); err != nil {
			return err
		}
		if r.reported, err = r.reported.Add(cost.Reported); err != nil {
			return err
		}
		if r.estimated, err = r.estimated.Add(cost.Estimated); err != nil {
			return err
		}
		addUsage(&r.unpriced, cost.Unpriced)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO rollup_usage_daily
			(day, agent_id, workspace_id, model, sessions, messages,
			 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			 cost_usd, cost_reported_usd, cost_estimated_usd,
			 cost_nanos, cost_reported_nanos, cost_estimated_nanos,
			 unpriced_input_tokens, unpriced_output_tokens,
			 unpriced_cache_read_tokens, unpriced_cache_write_tokens, priced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, k := range order {
		r := daily[k]
		if _, err := stmt.ExecContext(ctx, r.day, r.agentID, r.workspaceID,
			r.model, r.sessions, r.messages, r.in, r.out, r.cr, r.cw,
			r.cost.USD(), r.reported.USD(), r.estimated.USD(),
			int64(r.cost), int64(r.reported), int64(r.estimated),
			r.unpriced.InputTokens, r.unpriced.OutputTokens,
			r.unpriced.CacheReadTokens, r.unpriced.CacheWriteTokens,
			boolInt(usageTotal(r.unpriced) == 0)); err != nil {
			return fmt.Errorf("writing rollup: %w", err)
		}
	}
	if fingerprint := PricerFingerprint(pricer); fingerprint != "" {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			pricingFingerprintMeta, fingerprint); err != nil {
			return fmt.Errorf("recording pricing fingerprint: %w", err)
		}
	}
	for _, q := range []string{`DELETE FROM dirty_sessions`, `INSERT OR REPLACE INTO meta(key,value) VALUES ('rollups_full','0')`, `DROP TABLE IF EXISTS temp.ccpeek_dirty_days`} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return tx.Commit()
}
