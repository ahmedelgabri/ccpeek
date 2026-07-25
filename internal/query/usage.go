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
	Until   string // INCLUSIVE YYYY-MM-DD
	Limit   int
}

// group resolves a UsageFilter's dimension to its SQL group and order
// expressions. Usage and distinctUsageSessions MUST group identically:
// the second exists only to recompute each of the first's groups' session
// counts, and it keys the result by the group STRING. Two independently
// written switches that drifted would not error — they would attach
// session counts to the wrong rows.
func (f UsageFilter) group() (groupExpr, orderExpr string, err error) {
	switch f.GroupBy {
	case "", "day":
		return "r.day", "r.day DESC", nil
	case "model":
		return "r.model", "SUM(r.cost_usd) DESC", nil
	case "agent":
		return "a.slug", "SUM(r.cost_usd) DESC", nil
	case "project":
		return "COALESCE(w.canonical_path, '')", "SUM(r.cost_usd) DESC", nil
	}
	return "", "", fmt.Errorf("%w: unknown group %q (want day|model|project|agent)",
		ErrBadRequest, f.GroupBy)
}

// where builds the filter clause both queries run. Same reason as group():
// a filter applied to one and not the other misattributes session counts
// rather than failing.
func (f UsageFilter) where() (clause string, args []any) {
	clause = "WHERE 1=1"
	if f.Agent != "" {
		clause += " AND a.slug = ?"
		args = append(args, f.Agent)
	}
	if f.Model != "" {
		clause += " AND r.model = ?"
		args = append(args, f.Model)
	}
	if f.Since != "" {
		clause += " AND r.day >= ?"
		args = append(args, f.Since)
	}
	if f.Until != "" {
		clause += " AND r.day < ?"
		args = append(args, exclusiveUntil(f.Until))
	}
	return clause, args
}

// Usage aggregates the daily rollups by the requested dimension.
func (s *Service) Usage(ctx context.Context, f UsageFilter) ([]UsageRow, error) {
	groupExpr, orderExpr, err := f.group()
	if err != nil {
		return nil, err
	}
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	if err := checkPaging(f.Limit, 0); err != nil {
		return nil, err
	}
	// Usage is an aggregate surface: its group cardinality is naturally
	// bounded (days, models, agents, workspaces), so the default is ALL
	// groups — totals, charts, and CSV exports must never be silently
	// partial. A positive limit remains an explicit, caller-owned bound.
	if f.Limit < 0 {
		f.Limit = 0
	}

	where, args := f.where()
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
	// The caller already validated GroupBy through Usage.
	groupExpr, _, _ := f.group()

	where, args := f.where()

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
