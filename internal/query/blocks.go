package query

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// BlockRow is one 5-hour quota window — how Claude subscription limits are
// actually experienced (docs/v2-plan.md §7 P0 "blocks view").
type BlockRow struct {
	Start          string      `json:"start"` // RFC3339 UTC window start
	End            string      `json:"end"`
	Sessions       int64       `json:"sessions"`
	Messages       int64       `json:"messages"`
	Tokens         TokenTotals `json:"tokens"`
	CostUSD        float64     `json:"costUSD"`
	UnpricedTokens int64       `json:"unpricedTokens,omitempty"`
	Active         bool        `json:"active"` // the window containing now
}

const blockSeconds = 5 * 60 * 60

// Blocks aggregates usage into fixed UTC-aligned 5-hour windows, newest
// first, priced in auto mode (reported costs preferred, computed
// otherwise). limit bounds the number of windows returned.
func (s *Service) Blocks(ctx context.Context, agent string, limit int) ([]BlockRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 24
	}
	where := ""
	args := []any{}
	if agent != "" {
		where = "AND a.slug = ?"
		args = append(args, agent)
	}

	// created_at is RFC3339 text; unixepoch() handles it in SQLite ≥3.38.
	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT (unixepoch(m.created_at) / %d) AS win, m.model,
		       COUNT(DISTINCT se.id), COUNT(*),
		       SUM(u.input_tokens), SUM(u.output_tokens),
		       SUM(u.cache_read_tokens), SUM(u.cache_write_tokens),
		       SUM(COALESCE(u.reported_cost_usd, 0)),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.input_tokens ELSE 0 END),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.output_tokens ELSE 0 END),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_read_tokens ELSE 0 END),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_write_tokens ELSE 0 END)
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions se ON se.id = m.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE m.created_at IS NOT NULL %s
		GROUP BY win, m.model
		ORDER BY win DESC`, blockSeconds, where), args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating blocks: %w", err)
	}
	defer rows.Close()

	byWin := map[int64]*BlockRow{}
	sessionsMax := map[int64]int64{}
	for rows.Next() {
		var win, sessions, messages int64
		var model string
		var in, out, cr, cw int64
		var reported float64
		var uin, uout, ucr, ucw int64
		if err := rows.Scan(&win, &model, &sessions, &messages,
			&in, &out, &cr, &cw, &reported,
			&uin, &uout, &ucr, &ucw); err != nil {
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
		// Session counts per (win, model) overlap across models; keep the
		// max as a floor rather than double-counting.
		if sessions > sessionsMax[win] {
			sessionsMax[win] = sessions
		}
		b.Messages += messages
		b.Tokens.Input += in
		b.Tokens.Output += out
		b.Tokens.CacheRead += cr
		b.Tokens.CacheWrite += cw
		b.CostUSD += reported
		if uin+uout+ucr+ucw > 0 {
			if rate, ok := s.pricer.Lookup(model); ok {
				b.CostUSD += rate.Cost(canon.Usage{
					InputTokens: uin, OutputTokens: uout,
					CacheReadTokens: ucr, CacheWriteTokens: ucw,
				})
			} else {
				b.UnpricedTokens += uin + uout + ucr + ucw
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	nowWin := time.Now().UTC().Unix() / blockSeconds
	wins := make([]int64, 0, len(byWin))
	for w := range byWin {
		wins = append(wins, w)
	}
	sort.Slice(wins, func(i, j int) bool { return wins[i] > wins[j] })
	if len(wins) > limit {
		wins = wins[:limit]
	}
	out := make([]BlockRow, 0, len(wins))
	for _, w := range wins {
		b := byWin[w]
		b.Sessions = sessionsMax[w]
		b.Active = w == nowWin
		out = append(out, *b)
	}
	return out, nil
}
