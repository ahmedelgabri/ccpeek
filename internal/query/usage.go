package query

import (
	"context"
	"fmt"
)

// UsageRow is one group of the `usage` op, straight from the rollups.
type UsageRow struct {
	Group    string      `json:"group"` // day, model, project path, or agent slug
	Sessions int64       `json:"sessions"`
	Messages int64       `json:"messages"`
	Tokens   TokenTotals `json:"tokens"`
	CostUSD  float64     `json:"costUSD"`
	// CostUSD = reported (the agent's own figure) + estimated (priced from
	// tokens) — the closest available proxy for API-billed vs
	// subscription-covered spend.
	CostReportedUSD  float64 `json:"costReportedUSD"`
	CostEstimatedUSD float64 `json:"costEstimatedUSD"`
	HasUnpriced      bool    `json:"hasUnpriced,omitempty"`
}

// UsageFilter narrows the usage op.
type UsageFilter struct {
	GroupBy string // day | model | project | agent
	Agent   string
	Model   string
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
		return nil, fmt.Errorf("%w: unknown group %q (want day|model|project|agent)",
			ErrBadRequest, f.GroupBy)
	}
	// Usage is an aggregate surface: its group cardinality is naturally
	// bounded (days, models, agents, workspaces), so the default is ALL
	// groups — totals, charts, and CSV exports must never be silently
	// partial. A positive limit remains an explicit, caller-owned bound.
	if f.Limit < 0 {
		f.Limit = 0
	}

	where := "WHERE 1=1"
	var args []any
	if f.Agent != "" {
		where += " AND a.slug = ?"
		args = append(args, f.Agent)
	}
	if f.Model != "" {
		where += " AND r.model = ?"
		args = append(args, f.Model)
	}
	if f.Since != "" {
		where += " AND r.day >= ?"
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where += " AND r.day < ?"
		args = append(args, f.Until)
	}
	limitClause := ""
	if f.Limit > 0 {
		limitClause = "LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT %s AS grp,
		       SUM(r.sessions), SUM(r.messages),
		       SUM(r.input_tokens), SUM(r.output_tokens),
		       SUM(r.cache_read_tokens), SUM(r.cache_write_tokens),
		       SUM(r.cost_usd), SUM(r.cost_reported_usd),
		       SUM(r.cost_estimated_usd),
		       MIN(r.priced)
		FROM rollup_usage_daily r
		JOIN agents a ON a.id = r.agent_id
		LEFT JOIN workspaces w ON w.id = r.workspace_id
		%s
		GROUP BY grp
		ORDER BY %s
		%s`, groupExpr, where, orderExpr, limitClause), args...)
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
			&r.CostUSD, &r.CostReportedUSD, &r.CostEstimatedUSD,
			&minPriced); err != nil {
			return nil, err
		}
		r.HasUnpriced = minPriced == 0
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Session counts are NOT additive across rollup rows: a session that
	// spans models (or days, for the non-day groups) appears in several
	// rows, and SUM would count it once per row. Recompute the counts as
	// true distinct sessions per group from message usage.
	distinct, err := s.distinctUsageSessions(ctx, f)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Sessions = distinct[out[i].Group]
	}
	return out, nil
}

// distinctUsageSessions counts distinct usage-bearing sessions per group.
//
// It reads rollup_session_days, which carries the same dimensions and the
// same filters as the aggregate above. That matters for more than tidiness:
// the previous implementation re-derived the answer from message_usage ⋈
// messages ⋈ sessions with the day filter written as a substr() over
// created_at, so no index applied and every Usage call — including the
// /api/v1/budget read the Overview page issues on mount — paid a full scan
// of the corpus the rollups exist to avoid.
func (s *Service) distinctUsageSessions(ctx context.Context, f UsageFilter) (map[string]int64, error) {
	var groupExpr string
	switch f.GroupBy {
	case "", "day":
		groupExpr = "r.day"
	case "model":
		groupExpr = "r.model"
	case "agent":
		groupExpr = "a.slug"
	case "project":
		groupExpr = "COALESCE(w.canonical_path, '')"
	}

	where := "WHERE 1=1"
	var args []any
	if f.Agent != "" {
		where += " AND a.slug = ?"
		args = append(args, f.Agent)
	}
	if f.Model != "" {
		where += " AND r.model = ?"
		args = append(args, f.Model)
	}
	if f.Since != "" {
		where += " AND r.day >= ?"
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where += " AND r.day < ?"
		args = append(args, f.Until)
	}

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT %s AS grp, COUNT(DISTINCT r.session_id)
		FROM rollup_session_days r
		JOIN agents a ON a.id = r.agent_id
		LEFT JOIN workspaces w ON w.id = r.workspace_id
		%s
		GROUP BY grp`, groupExpr, where), args...)
	if err != nil {
		return nil, fmt.Errorf("counting distinct sessions: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var grp string
		var n int64
		if err := rows.Scan(&grp, &n); err != nil {
			return nil, err
		}
		out[grp] = n
	}
	return out, rows.Err()
}
