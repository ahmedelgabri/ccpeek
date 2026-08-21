package query

import (
	"context"
	"fmt"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// UsageRow is one group of the `usage` op, straight from the rollups.
type UsageRow struct {
	Group        string      `json:"group"` // day, model, project path, or agent slug
	Sessions     int64       `json:"sessions"`
	Messages     int64       `json:"messages"`
	Tokens       TokenTotals `json:"tokens"`
	CostUSD      float64     `json:"costUSD"`
	CostUSDExact string      `json:"costUSDExact"`
	CostMode     string      `json:"costMode"`
	// CostUSD = reported (the agent's own figure) + estimated (priced from
	// tokens) — the closest available proxy for API-billed vs
	// subscription-covered spend.
	CostReportedUSD       float64      `json:"costReportedUSD"`
	CostReportedUSDExact  string       `json:"costReportedUSDExact"`
	CostEstimatedUSD      float64      `json:"costEstimatedUSD"`
	CostEstimatedUSDExact string       `json:"costEstimatedUSDExact"`
	HasUnpriced           bool         `json:"hasUnpriced,omitempty"`
	UnpricedTokenTypes    *TokenTotals `json:"unpricedTokenTypes,omitempty"`
	HasUnreported         bool         `json:"hasUnreported,omitempty"`
	UnreportedTokenTypes  *TokenTotals `json:"unreportedTokenTypes,omitempty"`
}

// UsageFilter narrows the usage op.
type UsageFilter struct {
	GroupBy  string // day | model | project | agent
	Agent    string
	Model    string
	Since    string // inclusive YYYY-MM-DD
	Until    string // INCLUSIVE YYYY-MM-DD
	Limit    int
	CostMode string // auto | calculate | display; empty = auto
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

type usageCostColumns struct {
	amount, reported, estimated string
	unpriced                    [4]string
	unreported                  [4]string
}

func tokenTotal(tokens TokenTotals) int64 {
	return max(tokens.Input, 0) + max(tokens.Output, 0) + max(tokens.CacheRead, 0) + max(tokens.CacheWrite, 0)
}

func columnsForMode(mode db.CostMode) usageCostColumns {
	zero := "0"
	switch mode {
	case db.CostModeCalculate:
		return usageCostColumns{
			amount: "r.cost_calculated_nanos", reported: zero, estimated: "r.cost_calculated_nanos",
			unpriced:   [4]string{"r.calculated_unpriced_input_tokens", "r.calculated_unpriced_output_tokens", "r.calculated_unpriced_cache_read_tokens", "r.calculated_unpriced_cache_write_tokens"},
			unreported: [4]string{zero, zero, zero, zero},
		}
	case db.CostModeDisplay:
		return usageCostColumns{
			amount: "r.cost_reported_nanos", reported: "r.cost_reported_nanos", estimated: zero,
			unpriced:   [4]string{zero, zero, zero, zero},
			unreported: [4]string{"r.unreported_input_tokens", "r.unreported_output_tokens", "r.unreported_cache_read_tokens", "r.unreported_cache_write_tokens"},
		}
	default:
		return usageCostColumns{
			amount: "r.cost_nanos", reported: "r.cost_reported_nanos", estimated: "r.cost_estimated_nanos",
			unpriced:   [4]string{"r.unpriced_input_tokens", "r.unpriced_output_tokens", "r.unpriced_cache_read_tokens", "r.unpriced_cache_write_tokens"},
			unreported: [4]string{zero, zero, zero, zero},
		}
	}
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
	mode, err := db.ParseCostMode(f.CostMode)
	if err != nil {
		return nil, badRequest(err.Error())
	}
	columns := columnsForMode(mode)
	groupExpr, orderExpr, err := f.group()
	if err != nil {
		return nil, err
	}
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	// Usage is an aggregate surface: its group cardinality is naturally
	// bounded (days, models, agents, workspaces), so the default is ALL
	// groups — totals, charts, and CSV exports must never be silently
	// partial. A positive limit remains an explicit, caller-owned bound.
	if err := UsageLimit.apply(&f.Limit); err != nil {
		return nil, err
	}

	if f.GroupBy != "" && f.GroupBy != "day" {
		orderExpr = "SUM(" + columns.amount + ") DESC"
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
		       SUM(%s), SUM(%s), SUM(%s),
		       SUM(%s), SUM(%s), SUM(%s), SUM(%s),
		       SUM(%s), SUM(%s), SUM(%s), SUM(%s)
		FROM rollup_usage_daily r
		JOIN agents a ON a.id = r.agent_id
		LEFT JOIN workspaces w ON w.id = r.workspace_id
		%s
		GROUP BY grp
		ORDER BY %s
		%s`, groupExpr,
		columns.amount, columns.reported, columns.estimated,
		columns.unpriced[0], columns.unpriced[1], columns.unpriced[2], columns.unpriced[3],
		columns.unreported[0], columns.unreported[1], columns.unreported[2], columns.unreported[3],
		where, orderExpr, limitClause), args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating usage: %w", err)
	}
	defer rows.Close()

	var out []UsageRow
	for rows.Next() {
		var r UsageRow
		var amount, reported, estimated pricing.Amount
		var unpriced, unreported TokenTotals
		if err := rows.Scan(&r.Group, &r.Sessions, &r.Messages,
			&r.Tokens.Input, &r.Tokens.Output,
			&r.Tokens.CacheRead, &r.Tokens.CacheWrite,
			&amount, &reported, &estimated,
			&unpriced.Input, &unpriced.Output,
			&unpriced.CacheRead, &unpriced.CacheWrite,
			&unreported.Input, &unreported.Output,
			&unreported.CacheRead, &unreported.CacheWrite); err != nil {
			return nil, err
		}
		r.CostUSD = amount.USD()
		r.CostUSDExact = amount.String()
		r.CostMode = string(mode)
		r.CostReportedUSD = reported.USD()
		r.CostReportedUSDExact = reported.String()
		r.CostEstimatedUSD = estimated.USD()
		r.CostEstimatedUSDExact = estimated.String()
		r.HasUnpriced = tokenTotal(unpriced) > 0
		if r.HasUnpriced {
			r.UnpricedTokenTypes = &unpriced
		}
		r.HasUnreported = tokenTotal(unreported) > 0
		if r.HasUnreported {
			r.UnreportedTokenTypes = &unreported
		}
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
// one the Overview page issues on mount — paid a full scan of the corpus
// the rollups exist to avoid.
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
