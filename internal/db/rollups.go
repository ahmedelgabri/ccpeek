package db

import (
	"context"
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

// needsEstimatedCost is the SQL definition of auto mode. A reported zero with
// non-zero usage is provenance, not a usable API-equivalent value: preserve it
// in storage but calculate the row. A genuine zero-token reported zero remains
// reported and does not manufacture an unpriced model warning.
const needsEstimatedCost = `(u.reported_cost_usd IS NULL OR
	(u.reported_cost_usd = 0 AND
	 (u.input_tokens > 0 OR u.output_tokens > 0 OR
	  u.cache_read_tokens > 0 OR u.cache_write_tokens > 0)))`

// EstimatedTokenSums are the five token columns restricted to rows auto mode
// must calculate. Cache-write 1h is a subset of cache-write total. Every raw
// usage surface selects exactly these, in this order, and feeds them to
// AutoCost.
const EstimatedTokenSums = `SUM(CASE WHEN ` + needsEstimatedCost + ` THEN u.input_tokens ELSE 0 END),
	SUM(CASE WHEN ` + needsEstimatedCost + ` THEN u.output_tokens ELSE 0 END),
	SUM(CASE WHEN ` + needsEstimatedCost + ` THEN u.cache_read_tokens ELSE 0 END),
	SUM(CASE WHEN ` + needsEstimatedCost + ` THEN u.cache_write_tokens ELSE 0 END),
	SUM(CASE WHEN ` + needsEstimatedCost + ` THEN u.cache_write_1h_tokens ELSE 0 END)`

// ReportedCostSum excludes rows whose zero report falls through to estimation.
// Numerically zero would not change the sum, but spelling the same predicate
// makes the reported/estimated decision auditable and robust to future modes.
const ReportedCostSum = `SUM(CASE WHEN ` + needsEstimatedCost + `
	THEN 0 ELSE COALESCE(u.reported_cost_usd, 0) END)`

// AutoCost prices one normalized aggregate. It reports the cost of known
// buckets, tokens left unpriced because the model or a bucket rate is absent,
// and whether every non-zero bucket was priced. A zero-token row is always
// fully priced: it cannot make a Usage group falsely claim unpriced tokens.
func AutoCost(p Pricer, provider, model string, u canon.Usage) (cost float64, unpriced canon.Usage, fullyPriced bool) {
	total := usageTotal(u)
	if total == 0 {
		return 0, canon.Usage{}, true
	}
	rate, ok := p.Lookup(PricingModel(provider, model))
	if !ok {
		return 0, canon.Usage{
			InputTokens: max(u.InputTokens, 0), OutputTokens: max(u.OutputTokens, 0),
			CacheReadTokens: max(u.CacheReadTokens, 0), CacheWriteTokens: max(u.CacheWriteTokens, 0),
			CacheWrite1hTokens: min(max(u.CacheWrite1hTokens, 0), max(u.CacheWriteTokens, 0)),
		}, false
	}
	cost, unpriced = rate.Price(u)
	return cost, unpriced, usageTotal(unpriced) == 0
}

func usageTotal(u canon.Usage) int64 {
	return max(u.InputTokens, 0) + max(u.OutputTokens, 0) +
		max(u.CacheReadTokens, 0) + max(u.CacheWriteTokens, 0)
}

// RollupsNeedRegeneration reports whether materialized cost was built with a
// different pricing snapshot/algorithm. Empty rollups with usage also rebuild,
// preserving the migration self-heal behavior.
func (s *Store) RollupsNeedRegeneration(ctx context.Context, p Pricer) (bool, error) {
	var rollups, usage int
	var stored string
	if err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM rollup_usage_daily),
		       (SELECT COUNT(*) FROM message_usage),
		       COALESCE((SELECT value FROM meta WHERE key = ?), '')`,
		pricingFingerprintMeta).Scan(&rollups, &usage, &stored); err != nil {
		return false, err
	}
	if usage == 0 {
		return false, nil
	}
	if rollups == 0 {
		return true, nil
	}
	fingerprint := PricerFingerprint(p)
	return fingerprint != "" && fingerprint != stored, nil
}

// RegenerateRollups fully rebuilds both usage materializations in auto mode.
// Reported non-zero costs win per row; missing and zero-with-usage reports are
// calculated. Missing model or cache-bucket rates remain visible as unpriced.
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

	// One scan groups by provider as well as the materialized display
	// dimensions so provider-specific prices apply. The Go fold drops provider
	// again, preserving the public model grouping while adding costs correctly.
	rows, err := tx.QueryContext(ctx, `
		SELECT
			substr(COALESCE(m.created_at, s.created_at, ''), 1, 10) AS day,
			s.agent_id,
			COALESCE(sw.workspace_id, 0) AS workspace_id,
			m.provider,
			m.model,
			s.id,
			COUNT(*),
			SUM(u.input_tokens),
			SUM(u.output_tokens),
			SUM(u.cache_read_tokens),
			SUM(u.cache_write_tokens),
			`+ReportedCostSum+`,
			`+EstimatedTokenSums+`,
			SUM(CASE WHEN `+needsEstimatedCost+` THEN 1 ELSE 0 END)
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions s ON s.id = m.session_id
		LEFT JOIN session_workspaces sw ON sw.session_id = s.id
		GROUP BY day, s.agent_id, workspace_id, m.provider, m.model, s.id`)
	if err != nil {
		return fmt.Errorf("aggregating usage: %w", err)
	}
	defer rows.Close()

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
		unpriced            canon.Usage
		seenSessions        map[int64]bool
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
		var k key
		var provider string
		var sessionID int64
		var messages, in, out, cr, cw int64
		var reported float64
		var uin, uout, ucr, ucw, ucw1h, estimatedRows int64
		if err := rows.Scan(&k.day, &k.agentID, &k.workspaceID, &provider,
			&k.model, &sessionID, &messages, &in, &out, &cr, &cw,
			&reported, &uin, &uout, &ucr, &ucw, &ucw1h, &estimatedRows); err != nil {
			return err
		}
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
		r.messages += messages
		r.in += in
		r.out += out
		r.cr += cr
		r.cw += cw
		r.reported += reported
		if estimatedRows > 0 {
			cost, unpriced, _ := AutoCost(pricer, provider, k.model, canon.Usage{
				InputTokens: uin, OutputTokens: uout, CacheReadTokens: ucr,
				CacheWriteTokens: ucw, CacheWrite1hTokens: ucw1h,
			})
			r.estimated += cost
			r.unpriced.InputTokens += unpriced.InputTokens
			r.unpriced.OutputTokens += unpriced.OutputTokens
			r.unpriced.CacheReadTokens += unpriced.CacheReadTokens
			r.unpriced.CacheWriteTokens += unpriced.CacheWriteTokens
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
			 cost_estimated_usd, unpriced_input_tokens,
			 unpriced_output_tokens, unpriced_cache_read_tokens,
			 unpriced_cache_write_tokens, priced)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, k := range order {
		r := daily[k]
		if _, err := stmt.ExecContext(ctx, r.day, r.agentID, r.workspaceID,
			r.model, r.sessions, r.messages, r.in, r.out, r.cr, r.cw,
			r.reported+r.estimated, r.reported, r.estimated,
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
	return tx.Commit()
}
