package query

// This file holds the dashboard, command-browser, and per-session tool
// ops — the surfaces that show relations between entities (sessions ↔
// commands ↔ files ↔ workspaces) rather than flat lists.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// AgentStat is one agent's slice of the overview.
type AgentStat struct {
	Agent          string  `json:"agent"`
	Sessions       int     `json:"sessions"`
	LastActive     string  `json:"lastActive,omitempty"`
	Tokens         int64   `json:"tokens"`
	CostUSD        float64 `json:"costUSD"`
	CostUSDExact   string  `json:"costUSDExact"`
	UnpricedTokens int64   `json:"unpricedTokens,omitempty"`
}

// DayActivity is one day of the activity heatmap.
type DayActivity struct {
	Day            string  `json:"day"`
	Sessions       int     `json:"sessions"`
	CostUSD        float64 `json:"costUSD"`
	CostUSDExact   string  `json:"costUSDExact"`
	UnpricedTokens int64   `json:"unpricedTokens,omitempty"`
}

// WorkspaceStat is one workspace facet row of the overview.
type WorkspaceStat struct {
	Path       string `json:"path"`
	Sessions   int    `json:"sessions"`
	LastActive string `json:"lastActive,omitempty"`
}

// FileTouch is one recently-modified file in the edits feed.
type FileTouch struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"` // file_write | file_edit
	Agent     string `json:"agent"`
	SessionID string `json:"sessionId"`
	At        string `json:"at,omitempty"`
}

// KindCount is one slice of the tool-call distribution.
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// Stats is the overview: headline counts plus the relation facets the
// dashboard renders (per-agent, per-day activity, workspaces, recent
// file edits).
type Stats struct {
	Sessions       int     `json:"sessions"`
	Messages       int     `json:"messages"`
	ToolCalls      int     `json:"toolCalls"`
	Commands       int     `json:"commands"`
	Artifacts      int     `json:"artifacts"`
	ScanFindings   int     `json:"scanFindings"`
	Tokens         int64   `json:"tokens"`
	CostUSD        float64 `json:"costUSD"`
	CostUSDExact   string  `json:"costUSDExact"`
	UnpricedTokens int64   `json:"unpricedTokens,omitempty"`
	// CostMonthUSD is the current calendar month (UTC rollup days).
	CostMonthUSD            float64 `json:"costMonthUSD"`
	CostMonthUSDExact       string  `json:"costMonthUSDExact"`
	CostMonthUnpricedTokens int64   `json:"costMonthUnpricedTokens,omitempty"`

	Agents      []AgentStat     `json:"agents,omitempty"`
	Activity    []DayActivity   `json:"activity,omitempty"`
	Workspaces  []WorkspaceStat `json:"workspaces,omitempty"`
	RecentFiles []FileTouch     `json:"recentFiles,omitempty"`
	ToolKinds   []KindCount     `json:"toolKinds,omitempty"`
}

// Stats builds the overview with automatic reported-first cost.
func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	const costColumn = "cost_nanos"
	const unpricedExpr = "r.unpriced_input_tokens + r.unpriced_output_tokens + r.unpriced_cache_read_tokens + r.unpriced_cache_write_tokens"
	rdb := s.store.ReadDB()
	st := &Stats{}

	// Scan findings count only ACTIVE ones: a finding the user ignored must
	// not keep the overview tile in its warning state. The ignore-match
	// rule lives in db so every surface reading it agrees.
	//
	// The commands count carries the SAME `command <> ''` predicate the
	// Commands list applies: a shell call whose command text never
	// normalized is not browsable, so counting it made the tile promise
	// rows the browser can never show — the ArtifactKinds mismatch again,
	// a count answering a different question than the list beneath it.
	err := rdb.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM sessions),
		       (SELECT COUNT(*) FROM messages),
		       (SELECT COUNT(*) FROM tool_calls),
		       (SELECT COUNT(*) FROM tool_calls WHERE kind = 'shell' AND command <> ''),
		       (SELECT COUNT(*) FROM artifacts),
		       (SELECT COUNT(*) FROM scan_findings f WHERE NOT `+db.ScanIgnoredSQL("f")+`)`).
		Scan(&st.Sessions, &st.Messages, &st.ToolCalls, &st.Commands,
			&st.Artifacts, &st.ScanFindings)
	if err != nil {
		return nil, fmt.Errorf("overview counts: %w", err)
	}

	var totalAmount, monthAmount pricing.Amount
	err = rdb.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0),
		       COALESCE(SUM(%s), 0),
		       COALESCE(SUM(CASE WHEN day >= strftime('%%Y-%%m-01', 'now') THEN %s ELSE 0 END), 0),
		       COALESCE(SUM(%s), 0),
		       COALESCE(SUM(CASE WHEN day >= strftime('%%Y-%%m-01', 'now') THEN %s ELSE 0 END), 0)
		FROM rollup_usage_daily r`, costColumn, costColumn,
		unpricedExpr, unpricedExpr)).
		Scan(&st.Tokens, &totalAmount, &monthAmount,
			&st.UnpricedTokens, &st.CostMonthUnpricedTokens)
	if err != nil {
		return nil, fmt.Errorf("overview totals: %w", err)
	}
	st.CostUSD, st.CostUSDExact = totalAmount.USD(), totalAmount.String()
	st.CostMonthUSD, st.CostMonthUSDExact = monthAmount.USD(), monthAmount.String()

	// Per-agent: session counts from the hub, usage from the rollups.
	rows, err := rdb.QueryContext(ctx, fmt.Sprintf(`
		SELECT a.slug,
		       (SELECT COUNT(*) FROM sessions se WHERE se.agent_id = a.id),
		       COALESCE((SELECT MAX(se.modified_at) FROM sessions se WHERE se.agent_id = a.id), ''),
		       COALESCE((SELECT SUM(r.input_tokens + r.output_tokens + r.cache_read_tokens + r.cache_write_tokens)
		                 FROM rollup_usage_daily r WHERE r.agent_id = a.id), 0),
		       COALESCE((SELECT SUM(r.%s) FROM rollup_usage_daily r WHERE r.agent_id = a.id), 0),
		       COALESCE((SELECT SUM(%s) FROM rollup_usage_daily r WHERE r.agent_id = a.id), 0)
		FROM agents a
		ORDER BY 5 DESC, 2 DESC`, costColumn, unpricedExpr))
	if err != nil {
		return nil, fmt.Errorf("agent stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AgentStat
		var amount pricing.Amount
		if err := rows.Scan(&a.Agent, &a.Sessions, &a.LastActive, &a.Tokens,
			&amount, &a.UnpricedTokens); err != nil {
			return nil, err
		}
		a.CostUSD, a.CostUSDExact = amount.USD(), amount.String()
		if a.Sessions > 0 {
			st.Agents = append(st.Agents, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// Activity: sessions touched per day + that day's cost. 371 days
	// covers the UI's 52-week grid plus the partial week at each end.
	rows, err = rdb.QueryContext(ctx, fmt.Sprintf(`
		SELECT act.day, act.n, COALESCE(r.cost, 0),
		       COALESCE(r.unpriced, 0)
		FROM (SELECT substr(modified_at, 1, 10) AS day, COUNT(*) AS n
		      FROM sessions
		      WHERE modified_at >= date('now', '-371 days')
		      GROUP BY 1) act
		LEFT JOIN (SELECT day, SUM(%s) AS cost,
		                  SUM(%s) AS unpriced
		           FROM rollup_usage_daily r GROUP BY day) r ON r.day = act.day
		ORDER BY act.day`, costColumn, unpricedExpr))
	if err != nil {
		return nil, fmt.Errorf("activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d DayActivity
		var amount pricing.Amount
		if err := rows.Scan(&d.Day, &d.Sessions, &amount,
			&d.UnpricedTokens); err != nil {
			return nil, err
		}
		d.CostUSD, d.CostUSDExact = amount.USD(), amount.String()
		st.Activity = append(st.Activity, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	rows, err = rdb.QueryContext(ctx, `
		SELECT cwd, COUNT(*), COALESCE(MAX(modified_at), '')
		FROM sessions WHERE cwd <> ''
		GROUP BY cwd ORDER BY 3 DESC LIMIT 8`)
	if err != nil {
		return nil, fmt.Errorf("workspaces: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var w WorkspaceStat
		if err := rows.Scan(&w.Path, &w.Sessions, &w.LastActive); err != nil {
			return nil, err
		}
		st.Workspaces = append(st.Workspaces, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	rows, err = rdb.QueryContext(ctx, `
		SELECT kind, COUNT(*) FROM tool_calls GROUP BY kind ORDER BY 2 DESC`)
	if err != nil {
		return nil, fmt.Errorf("tool kinds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k KindCount
		if err := rows.Scan(&k.Kind, &k.Count); err != nil {
			return nil, err
		}
		st.ToolKinds = append(st.ToolKinds, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// Recent file edits: latest write/edit per path (agentsview-style feed).
	// Ordered on tc.started_at alone, and PINNED to
	// idx_tool_calls_recent_files, whose partial WHERE is exactly this
	// one. Left to itself the planner takes idx_tool_calls_kind for the
	// equality on kind and then sorts the whole result — every file
	// write/edit ever indexed — before the LIMIT can apply, on every
	// /stats call. The pin makes the index serve both the filter and the
	// order, so the LIMIT stops the scan; like the memory resolver's pin,
	// it also fails loudly if the index is ever renamed away.
	rows, err = rdb.QueryContext(ctx, `
		SELECT tc.file_path, tc.kind, a.slug, se.external_id,
		       COALESCE(tc.started_at, '')
		FROM tool_calls tc INDEXED BY `+db.IdxToolCallsRecentFiles+`
		JOIN sessions se ON se.id = tc.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE tc.kind IN ('file_write', 'file_edit') AND tc.file_path <> ''
		ORDER BY tc.started_at DESC LIMIT 120`)
	if err != nil {
		return nil, fmt.Errorf("recent files: %w", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var f FileTouch
		if err := rows.Scan(&f.Path, &f.Kind, &f.Agent, &f.SessionID, &f.At); err != nil {
			return nil, err
		}
		if seen[f.Path] {
			continue
		}
		seen[f.Path] = true
		st.RecentFiles = append(st.RecentFiles, f)
		if len(st.RecentFiles) >= 20 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return st, nil
}

// CommandRow is one shell command with its session context.
type CommandRow struct {
	Command   string `json:"command"`
	At        string `json:"at,omitempty"`
	Agent     string `json:"agent"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd,omitempty"`
}

// CommandsFilter narrows the commands op.
type CommandsFilter struct {
	Agent   string
	Project string // substring of the session cwd
	Query   string // substring of the command text
	Since   string
	Until   string // INCLUSIVE YYYY-MM-DD
	Limit   int
	Offset  int
}

// commandsFrom is the joins every reading of the commands query shares,
// up to and including the WHERE keyword — the caller appends its own
// clause. The count in EachCommand needs the joins without the
// projection, which is why the two are separate constants.
const commandsFrom = `
	FROM tool_calls tc
	JOIN sessions se ON se.id = tc.session_id
	JOIN agents a ON a.id = se.agent_id
	WHERE `

// commandsSelect is the row projection both readings share: the paged
// newest-first op below and the oldest-first export walk in EachCommand.
// One text, so a column added to one reading cannot go missing from the
// other.
const commandsSelect = `
	SELECT tc.command,
	       COALESCE(tc.started_at, ''),
	       a.slug, se.external_id, se.cwd` + commandsFrom

// clauses renders the filter as WHERE fragments and their arguments,
// shared by both readings so one filter cannot mean two different sets.
func (f CommandsFilter) clauses() ([]string, []any) {
	where := []string{
		`tc.kind = 'shell'`,
		`tc.command <> ''`,
	}
	var args []any
	if f.Agent != "" {
		where = append(where, `a.slug = ?`)
		args = append(args, f.Agent)
	}
	if f.Project != "" {
		where = append(where, `se.cwd LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Project)+"%")
	}
	if f.Query != "" {
		where = append(where, `tc.command LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Query)+"%")
	}
	// Ordered and filtered on tc.started_at ALONE, not a COALESCE with the
	// session's timestamp: no index can supply an expression, so the
	// COALESCE forced SQLite to materialize and sort every shell call in
	// the corpus before the LIMIT could apply. Every adapter sets a tool
	// call's timestamp, so the fallback never fired anyway; on
	// idx_tool_calls_kind (kind, started_at DESC) the LIMIT now
	// short-circuits.
	if f.Since != "" {
		where = append(where, `tc.started_at >= ?`)
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where = append(where, `tc.started_at < ?`)
		args = append(args, exclusiveUntil(f.Until))
	}
	return where, args
}

// scanCommands reads a command result set.
func scanCommands(rows *sql.Rows, fn func(CommandRow) error) error {
	defer rows.Close()
	for rows.Next() {
		var c CommandRow
		if err := rows.Scan(&c.Command, &c.At, &c.Agent, &c.SessionID, &c.CWD); err != nil {
			return err
		}
		if err := fn(c); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Commands lists shell commands newest-first across every agent.
func (s *Service) Commands(ctx context.Context, f CommandsFilter) ([]CommandRow, error) {
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	if err := checkOffset(f.Offset); err != nil {
		return nil, err
	}
	if err := CommandsLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	where, args := f.clauses()
	args = append(args, f.Limit, f.Offset)

	rows, err := s.store.ReadDB().QueryContext(ctx, commandsSelect+
		strings.Join(where, " AND ")+`
		ORDER BY tc.started_at DESC, tc.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing commands: %w", err)
	}
	var out []CommandRow
	err = scanCommands(rows, func(c CommandRow) error {
		out = append(out, c)
		return nil
	})
	return out, err
}

// EachCommand walks the commands matching f OLDEST FIRST — the order a
// shell history file is read in — handing each row to fn as it is read.
//
// This is the export reading, and it exists because both exporters (the
// CLI's `export commands` and HTTP's ?format=) had hand-rolled the same
// wrong shape: page the newest-first op to completion, buffer the WHOLE
// corpus, then walk it backwards. Every page re-produced and discarded
// its OFFSET rows, so exporting N commands cost O(N²/page) row
// production, and none of it reached the writer until all of it was in
// memory. One ascending query streams instead: rows arrive in the file's
// own order and go straight out.
//
// fn's error stops the walk and is returned as-is (a writer that fails
// has nowhere to put the rest).
//
// Limit and Offset keep the meaning they have on the paged op — the
// newest N, skipping the newest M — because that is what a caller who
// sends ?limit= means. Translating them onto an ascending walk needs the
// selection's size, so they cost one COUNT; an unbounded export (the
// normal one) pays nothing.
func (s *Service) EachCommand(ctx context.Context, f CommandsFilter, fn func(CommandRow) error) error {
	if err := checkWindow(f.Since, f.Until); err != nil {
		return err
	}
	if err := checkOffset(f.Offset); err != nil {
		return err
	}
	if f.Limit != 0 {
		// Applied only to enforce the ceiling — an over-cap limit is refused
		// here as it is on the paged op, never silently clipped. Zero is
		// left alone rather than resolved to the op default: an unbounded
		// export is the whole selection, not a page of it.
		if err := CommandsLimit.apply(&f.Limit); err != nil {
			return err
		}
	}

	where, args := f.clauses()
	clause := strings.Join(where, " AND ")
	bounds := ""
	if f.Limit > 0 || f.Offset > 0 {
		var total int
		if err := s.store.ReadDB().QueryRowContext(ctx,
			`SELECT COUNT(*)`+commandsFrom+clause, args...).Scan(&total); err != nil {
			return fmt.Errorf("counting commands: %w", err)
		}
		end := total - f.Offset // the newest row of the selection, exclusive
		start := 0
		if f.Limit > 0 {
			start = end - f.Limit
		}
		start = max(start, 0)
		if end <= start {
			return nil
		}
		bounds = "\n\t\tLIMIT ? OFFSET ?"
		args = append(args, end-start, start)
	}

	rows, err := s.store.ReadDB().QueryContext(ctx, commandsSelect+clause+`
		ORDER BY tc.started_at ASC, tc.id ASC`+bounds, args...)
	if err != nil {
		return fmt.Errorf("listing commands: %w", err)
	}
	return scanCommands(rows, fn)
}

// chipDetailCap bounds the detail column of a compact chip row: a chip
// label shows at most the first line's start, so shipping whole shell
// scripts per chip would defeat the compact projection.
const chipDetailCap = 120

// ToolCallRow is one tool invocation of a session, with just enough
// detail to browse (command text or file path, never the full payload).
// Change excerpts (diff old/new) deliberately do NOT ride list rows —
// they can reach 16 KiB each, so they ship only through the per-call
// detail lookup when a row is actually expanded.
type ToolCallRow struct {
	Seq int `json:"seq"`
	// MessageSeq is the transcript seq of the issuing message, letting the
	// UI attach tool chips to their message bubbles.
	MessageSeq int    `json:"messageSeq"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail,omitempty"` // shell command or file path
	Status     string `json:"status,omitempty"`
	At         string `json:"at,omitempty"`
}

// ToolCallDetail is one call's full browseable payload: the list row
// plus the change excerpts for diff rendering — old/new for file_edit,
// the written content as New for file_write (an all-addition diff).
type ToolCallDetail struct {
	ToolCallRow
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// ToolsFilter pages a session's tool calls. Limit <= 0 returns
// everything from Offset on — completeness is the default; the limit is
// an explicit page size for callers that stream large sessions.
// FromSeq/ToSeq bound the issuing MESSAGE seq (ToSeq 0 = unbounded), so
// the transcript can request chips for exactly its loaded range.
// Compact trims each row to chip size: detail capped, no timestamps.
type ToolsFilter struct {
	Limit   int
	Offset  int
	FromSeq int
	ToSeq   int
	Compact bool
}

// SessionTools returns one session's tool calls in order, paged by f.
func (s *Service) SessionTools(ctx context.Context, agentSlug, externalID string, f ToolsFilter) ([]ToolCallRow, error) {
	// Bounds are checked HERE, not in a transport: HTTP parsed and rejected
	// a negative limit/offset/seq while `ccpeek query tools --offset -5` and
	// the MCP tool passed it straight through, where a negative offset is
	// dropped and a negative seq bound matches everything — the full list
	// returned with a success status for a request that made no sense.
	// Checked before the session lookup, like Transcript's page size: a
	// malformed bound is a caller mistake whether or not the session exists.
	if err := checkOffset(f.Offset); err != nil {
		return nil, err
	}
	if err := ToolsLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	if err := checkSeq("from_seq", f.FromSeq); err != nil {
		return nil, err
	}
	if err := checkSeq("to_seq", f.ToSeq); err != nil {
		return nil, err
	}
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}
	detailExpr := `COALESCE(NULLIF(tc.command, ''), tc.file_path, '')`
	atExpr := `COALESCE(tc.started_at, '')`
	var args []any
	if f.Compact {
		detailExpr = fmt.Sprintf("substr(%s, 1, ?)", detailExpr)
		atExpr = `''`
		args = append(args, chipDetailCap)
	}
	where := "tc.session_id = ?"
	args = append(args, rowID)
	if f.FromSeq > 0 {
		where += " AND tc.message_seq >= ?"
		args = append(args, f.FromSeq)
	}
	if f.ToSeq > 0 {
		where += " AND tc.message_seq <= ?"
		args = append(args, f.ToSeq)
	}
	limitClause := ""
	if f.Limit > 0 {
		limitClause = "LIMIT ? OFFSET ?"
		args = append(args, f.Limit, f.Offset)
	} else if f.Offset > 0 {
		// SQLite requires a LIMIT before OFFSET; -1 means unbounded.
		limitClause = "LIMIT -1 OFFSET ?"
		args = append(args, f.Offset)
	}
	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT tc.seq, tc.message_seq, tc.name, tc.kind, %s,
		       tc.result_status, %s
		FROM tool_calls tc WHERE %s ORDER BY tc.seq %s`,
		detailExpr, atExpr, where, limitClause),
		args...)
	if err != nil {
		return nil, fmt.Errorf("listing tool calls: %w", err)
	}
	defer rows.Close()

	var out []ToolCallRow
	for rows.Next() {
		var t ToolCallRow
		if err := rows.Scan(&t.Seq, &t.MessageSeq, &t.Name, &t.Kind,
			&t.Detail, &t.Status, &t.At); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SessionToolDetail fetches one call with its change excerpts — the
// lazy counterpart to SessionTools, requested only when a row expands.
func (s *Service) SessionToolDetail(ctx context.Context, agentSlug, externalID string, seq int) (*ToolCallDetail, error) {
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}
	var d ToolCallDetail
	err = s.store.ReadDB().QueryRowContext(ctx, `
		SELECT tc.seq, tc.message_seq, tc.name, tc.kind,
		       COALESCE(NULLIF(tc.command, ''), tc.file_path, ''),
		       tc.result_status, COALESCE(tc.started_at, ''),
		       tc.old_text, tc.new_text
		FROM tool_calls tc WHERE tc.session_id = ? AND tc.seq = ?`,
		rowID, seq).
		Scan(&d.Seq, &d.MessageSeq, &d.Name, &d.Kind,
			&d.Detail, &d.Status, &d.At, &d.Old, &d.New)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: tool call %d of %s/%s", ErrNotFound, seq, agentSlug, externalID)
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// escapeLike escapes LIKE wildcards so user filters match literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// HistoryRow is one prompt-history entry — the retained cross-session
// prompt log (Claude's history.jsonl plus v1-imported entries).
type HistoryRow struct {
	Agent     string `json:"agent"`
	Display   string `json:"display"`
	At        string `json:"at,omitempty"`        // RFC3339 UTC
	SessionID string `json:"sessionId,omitempty"` // external id when linked
}

// HistoryFilter narrows the history op.
type HistoryFilter struct {
	Agent  string
	Query  string // substring of the prompt text
	Limit  int
	Offset int
}

// History lists prompt-history entries newest first.
func (s *Service) History(ctx context.Context, f HistoryFilter) ([]HistoryRow, error) {
	if err := checkOffset(f.Offset); err != nil {
		return nil, err
	}
	if err := HistoryLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	where := "WHERE h.display <> ''"
	var args []any
	if f.Agent != "" {
		where += " AND a.slug = ?"
		args = append(args, f.Agent)
	}
	if f.Query != "" {
		where += ` AND h.display LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Query)+"%")
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT a.slug, h.display, h.timestamp, COALESCE(se.external_id, '')
		FROM history h
		JOIN agents a ON a.id = h.agent_id
		LEFT JOIN sessions se ON se.id = h.session_id
		%s
		ORDER BY h.timestamp DESC, h.id DESC
		LIMIT ? OFFSET ?`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("listing history: %w", err)
	}
	defer rows.Close()
	var out []HistoryRow
	for rows.Next() {
		var r HistoryRow
		var ts int64
		if err := rows.Scan(&r.Agent, &r.Display, &ts, &r.SessionID); err != nil {
			return nil, err
		}
		if ts > 0 {
			r.At = time.UnixMilli(ts).UTC().Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
