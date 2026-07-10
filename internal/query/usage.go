package query

import (
	"context"
	"fmt"
)

// UsageRow is one group of the `usage` op, straight from the rollups.
type UsageRow struct {
	Group       string      `json:"group"` // day, model, project path, or agent slug
	Sessions    int64       `json:"sessions"`
	Messages    int64       `json:"messages"`
	Tokens      TokenTotals `json:"tokens"`
	CostUSD     float64     `json:"costUSD"`
	HasUnpriced bool        `json:"hasUnpriced,omitempty"`
}

// UsageFilter narrows the usage op.
type UsageFilter struct {
	GroupBy string // day | model | project | agent
	Agent   string
	Since   string // inclusive YYYY-MM-DD
	Until   string // exclusive YYYY-MM-DD
	Limit   int
}

// Usage aggregates the daily rollups by the requested dimension.
func (s *Service) Usage(ctx context.Context, f UsageFilter) ([]UsageRow, error) {
	var groupExpr, orderExpr string
	switch f.GroupBy {
	case "", "day":
		groupExpr, orderExpr = "r.day", "r.day DESC"
	case "model":
		groupExpr, orderExpr = "r.model", "SUM(r.cost_usd) DESC"
	case "agent":
		groupExpr, orderExpr = "a.slug", "SUM(r.cost_usd) DESC"
	case "project":
		groupExpr, orderExpr = "COALESCE(w.canonical_path, '')", "SUM(r.cost_usd) DESC"
	default:
		return nil, fmt.Errorf("unknown group %q (want day|model|project|agent)", f.GroupBy)
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}

	where := "WHERE 1=1"
	var args []any
	if f.Agent != "" {
		where += " AND a.slug = ?"
		args = append(args, f.Agent)
	}
	if f.Since != "" {
		where += " AND r.day >= ?"
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where += " AND r.day < ?"
		args = append(args, f.Until)
	}
	args = append(args, f.Limit)

	rows, err := s.store.DB().QueryContext(ctx, fmt.Sprintf(`
		SELECT %s AS grp,
		       SUM(r.sessions), SUM(r.messages),
		       SUM(r.input_tokens), SUM(r.output_tokens),
		       SUM(r.cache_read_tokens), SUM(r.cache_write_tokens),
		       SUM(r.cost_usd),
		       MIN(r.priced)
		FROM rollup_usage_daily r
		JOIN agents a ON a.id = r.agent_id
		LEFT JOIN workspaces w ON w.id = r.workspace_id
		%s
		GROUP BY grp
		ORDER BY %s
		LIMIT ?`, groupExpr, where, orderExpr), args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating usage: %w", err)
	}
	defer rows.Close()

	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		var minPriced int
		if err := rows.Scan(&r.Group, &r.Sessions, &r.Messages,
			&r.Tokens.Input, &r.Tokens.Output,
			&r.Tokens.CacheRead, &r.Tokens.CacheWrite,
			&r.CostUSD, &minPriced); err != nil {
			return nil, err
		}
		r.HasUnpriced = minPriced == 0
		out = append(out, r)
	}
	return out, rows.Err()
}
