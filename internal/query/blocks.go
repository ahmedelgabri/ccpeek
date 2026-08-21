package query

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// BlockRow is one FIXED, UTC-aligned 5-hour window of usage
// (docs/v2-plan.md §7 P0 "blocks view").
//
// It is an APPROXIMATION of a provider quota window, not the thing itself.
// Real subscription windows anchor to the first activity after an idle
// gap, so a session that starts at 12:30 UTC gets a 12:30–17:30 window
// while this view splits it across the 10:00–15:00 and 15:00–20:00
// buckets. Read a row as "usage in this wall-clock bucket"; in particular
// the Active row is what has been spent since the last bucket boundary,
// which can be well under what the live quota window is actually counting.
type BlockRow struct {
	Start                 string       `json:"start"` // RFC3339 UTC window start
	End                   string       `json:"end"`
	Sessions              int64        `json:"sessions"`
	Messages              int64        `json:"messages"`
	Tokens                TokenTotals  `json:"tokens"`
	CostUSD               float64      `json:"costUSD"`
	CostUSDExact          string       `json:"costUSDExact"`
	CostMode              string       `json:"costMode"`
	CostReportedUSD       float64      `json:"costReportedUSD"`
	CostReportedUSDExact  string       `json:"costReportedUSDExact"`
	CostEstimatedUSD      float64      `json:"costEstimatedUSD"`
	CostEstimatedUSDExact string       `json:"costEstimatedUSDExact"`
	UnpricedTokens        int64        `json:"unpricedTokens,omitempty"`
	UnpricedTokenTypes    *TokenTotals `json:"unpricedTokenTypes,omitempty"`
	UnreportedTokens      int64        `json:"unreportedTokens,omitempty"`
	UnreportedTokenTypes  *TokenTotals `json:"unreportedTokenTypes,omitempty"`
	// Active marks the bucket containing now — a partial bucket, not a
	// quota window's remaining allowance.
	Active bool `json:"active"`
}

const blockSeconds = 5 * 60 * 60

// Blocks aggregates usage into fixed UTC-aligned 5-hour windows, newest
// first, priced in auto mode (reported costs preferred, computed
// otherwise). limit bounds the number of windows returned.
//
// The buckets are epoch-aligned (00:00, 05:00, 10:00 … UTC) and nothing
// here anchors them to a session's first activity, which is how provider
// quota windows actually start — see BlockRow. The op documents itself the
// same way, so a caller reading "5-hour window" is not led to believe it is
// reading its live quota meter.
//
// The window bound is pushed into SQL rather than applied to the result.
// "What did my last few quota windows look like" is a recent-data
// question, but both aggregates used to scan the whole of message_usage —
// grouping on a computed unixepoch() expression, so no index applied —
// and then discard everything but the newest `limit` windows in Go. Years
// of history were read to answer about one day of it.
func (s *Service) Blocks(ctx context.Context, agent string, limit int) ([]BlockRow, error) {
	return s.BlocksWithCostMode(ctx, agent, limit, "")
}

// BlocksWithCostMode applies an explicit provenance mode to each usage row.
func (s *Service) BlocksWithCostMode(ctx context.Context, agent string, limit int, rawMode string) ([]BlockRow, error) {
	mode, err := db.ParseCostMode(rawMode)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	if err := BlocksLimit.apply(&limit); err != nil {
		return nil, err
	}
	agentFilter := ""
	var agentArgs []any
	if agent != "" {
		agentFilter = "AND a.slug = ?"
		agentArgs = append(agentArgs, agent)
	}

	// The cutoff is anchored on the newest indexed usage rather than on
	// wall-clock now: an archive whose last session was a month ago must
	// still show that month's windows, not an empty view. Everything after
	// the anchor is a bounded range on idx_messages_created.
	latest, err := s.latestUsageDay(ctx, agentFilter, agentArgs)
	if err != nil {
		return nil, err
	}
	if latest.IsZero() {
		return nil, nil
	}
	// One extra window of slack: the newest window is partial, so walking
	// back exactly `limit` windows from inside it can clip the oldest.
	cutoffWin := latest.Unix()/blockSeconds - int64(limit)
	cutoff := time.Unix(cutoffWin*blockSeconds, 0).UTC().Format(time.RFC3339)

	where := "AND m.created_at >= ? " + agentFilter
	args := append([]any{cutoff}, agentArgs...)

	// True distinct sessions per window, computed apart from the
	// per-model aggregation below: sessions that touch several models in
	// one window would otherwise be under-counted (the old max-per-model
	// figure was only a floor when the sets are disjoint).
	sessionsByWin := map[int64]int64{}
	srows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT (unixepoch(m.created_at) / %d) AS win, COUNT(DISTINCT se.id)
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions se ON se.id = m.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE m.created_at IS NOT NULL %s
		GROUP BY win`, blockSeconds, where), args...)
	if err != nil {
		return nil, fmt.Errorf("counting block sessions: %w", err)
	}
	for srows.Next() {
		var win, sessions int64
		if err := srows.Scan(&win, &sessions); err != nil {
			srows.Close()
			return nil, err
		}
		sessionsByWin[win] = sessions
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}

	// created_at is RFC3339 text; unixepoch() handles it in SQLite ≥3.38.
	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT (unixepoch(m.created_at) / %d) AS win, m.created_at,
		       m.provider, m.model,
		       u.input_tokens, u.output_tokens, u.cache_read_tokens,
		       u.cache_write_tokens, u.cache_write_1h_tokens,
		       `+db.ReportedCostNanosExpr+`
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions se ON se.id = m.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE m.created_at IS NOT NULL %s
		ORDER BY win DESC, m.id`, blockSeconds, where), args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating blocks: %w", err)
	}
	defer rows.Close()

	byWin := map[int64]*BlockRow{}
	amounts := map[int64]pricing.Amount{}
	reportedAmounts := map[int64]pricing.Amount{}
	estimatedAmounts := map[int64]pricing.Amount{}
	for rows.Next() {
		var win int64
		var occurredAt, provider, model string
		var usage canon.Usage
		var reported sql.NullInt64
		if err := rows.Scan(&win, &occurredAt, &provider, &model,
			&usage.InputTokens, &usage.OutputTokens, &usage.CacheReadTokens,
			&usage.CacheWriteTokens, &usage.CacheWrite1hTokens, &reported); err != nil {
			return nil, err
		}
		b := byWin[win]
		if b == nil {
			b = &BlockRow{
				Start: time.Unix(win*blockSeconds, 0).UTC().Format(time.RFC3339),
				End:   time.Unix((win+1)*blockSeconds, 0).UTC().Format(time.RFC3339),
			}
			byWin[win] = b
		}
		b.Messages++
		b.Tokens.Input += usage.InputTokens
		b.Tokens.Output += usage.OutputTokens
		b.Tokens.CacheRead += usage.CacheReadTokens
		b.Tokens.CacheWrite += usage.CacheWriteTokens
		var reportedAmount *pricing.Amount
		if reported.Valid {
			amount := pricing.Amount(reported.Int64)
			reportedAmount = &amount
		}
		result, err := db.EvaluateCostAt(s.pricer, mode, provider, model,
			db.ParseCostTime(occurredAt), usage, reportedAmount)
		if err != nil {
			return nil, err
		}
		if amounts[win], err = amounts[win].Add(result.Amount); err != nil {
			return nil, err
		}
		if reportedAmounts[win], err = reportedAmounts[win].Add(result.Reported); err != nil {
			return nil, err
		}
		if estimatedAmounts[win], err = estimatedAmounts[win].Add(result.Estimated); err != nil {
			return nil, err
		}
		addUnpriced(&b.UnpricedTokens, &b.UnpricedTokenTypes, result.Unpriced)
		addUnpriced(&b.UnreportedTokens, &b.UnreportedTokenTypes, result.Unreported)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	wins := make([]int64, 0, len(byWin))
	for w := range byWin {
		wins = append(wins, w)
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i] > wins[j] })
	if len(wins) > limit {
		wins = wins[:limit]
	}
	nowWin := time.Now().UTC().Unix() / blockSeconds
	out := make([]BlockRow, 0, len(wins))
	for _, w := range wins {
		b := byWin[w]
		b.Sessions = sessionsByWin[w]
		b.Active = w == nowWin
		b.CostUSD = amounts[w].USD()
		b.CostUSDExact = amounts[w].String()
		b.CostMode = string(mode)
		b.CostReportedUSD = reportedAmounts[w].USD()
		b.CostReportedUSDExact = reportedAmounts[w].String()
		b.CostEstimatedUSD = estimatedAmounts[w].USD()
		b.CostEstimatedUSDExact = estimatedAmounts[w].String()
		out = append(out, *b)
	}
	return out, nil
}

// latestUsageDay returns the newest day carrying usage, which anchors the
// blocks window. Empty when nothing is indexed.
//
// It reads rollup_session_days, NOT the message_usage join. SQLite's
// min/max optimisation only applies to a single-table FROM, so taking
// MAX(m.created_at) across that join walked idx_messages_created
// backwards probing three tables per row until it found one with usage —
// hundreds of milliseconds when the newest messages carry none, or when
// an agent filter matches nothing. Day granularity is enough: the window
// bound already carries a full window of slack.
func (s *Service) latestUsageDay(ctx context.Context, agentFilter string, args []any) (time.Time, error) {
	var raw sql.NullString
	row := s.store.ReadDB().QueryRowContext(ctx, fmt.Sprintf(`
		SELECT MAX(r.day) FROM rollup_session_days r
		JOIN agents a ON a.id = r.agent_id
		WHERE 1=1 %s`, agentFilter), args...)
	if err := row.Scan(&raw); err != nil {
		return time.Time{}, fmt.Errorf("finding newest usage: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse("2006-01-02", raw.String)
	if err != nil {
		// An unparseable day is not worth failing the view over; the caller
		// falls back to reporting no windows.
		return time.Time{}, nil
	}
	// The day's END, so the newest window inside it is inside the bound.
	return t.UTC().Add(24 * time.Hour), nil
}
