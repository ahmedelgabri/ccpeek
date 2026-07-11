package query

// This file holds the dashboard, command-browser, and per-session tool
// ops — the surfaces that show relations between entities (sessions ↔
// commands ↔ files ↔ workspaces) rather than flat lists.

import (
	"context"
	"fmt"
	"strings"
)

// AgentStat is one agent's slice of the overview.
type AgentStat struct {
	Agent      string  `json:"agent"`
	Sessions   int     `json:"sessions"`
	LastActive string  `json:"lastActive,omitempty"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"costUSD"`
}

// DayActivity is one day of the activity heatmap.
type DayActivity struct {
	Day      string  `json:"day"`
	Sessions int     `json:"sessions"`
	CostUSD  float64 `json:"costUSD"`
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
	Sessions     int     `json:"sessions"`
	Messages     int     `json:"messages"`
	ToolCalls    int     `json:"toolCalls"`
	Commands     int     `json:"commands"`
	Artifacts    int     `json:"artifacts"`
	ScanFindings int     `json:"scanFindings"`
	Tokens       int64   `json:"tokens"`
	CostUSD      float64 `json:"costUSD"`
	// CostMonthUSD is the current calendar month (UTC rollup days).
	CostMonthUSD float64 `json:"costMonthUSD"`

	Agents      []AgentStat     `json:"agents,omitempty"`
	Activity    []DayActivity   `json:"activity,omitempty"`
	Workspaces  []WorkspaceStat `json:"workspaces,omitempty"`
	RecentFiles []FileTouch     `json:"recentFiles,omitempty"`
	ToolKinds   []KindCount     `json:"toolKinds,omitempty"`
}

// Stats builds the overview in a handful of aggregate queries.
func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	db := s.store.DB()
	st := &Stats{}

	err := db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM sessions),
		       (SELECT COUNT(*) FROM messages),
		       (SELECT COUNT(*) FROM tool_calls),
		       (SELECT COUNT(*) FROM tool_calls WHERE kind = 'shell'),
		       (SELECT COUNT(*) FROM artifacts),
		       (SELECT COUNT(*) FROM scan_findings)`).
		Scan(&st.Sessions, &st.Messages, &st.ToolCalls, &st.Commands,
			&st.Artifacts, &st.ScanFindings)
	if err != nil {
		return nil, fmt.Errorf("overview counts: %w", err)
	}

	err = db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(input_tokens + output_tokens + cache_read_tokens + cache_write_tokens), 0),
		       COALESCE(SUM(cost_usd), 0),
		       COALESCE(SUM(CASE WHEN day >= strftime('%Y-%m-01', 'now') THEN cost_usd ELSE 0 END), 0)
		FROM rollup_usage_daily`).
		Scan(&st.Tokens, &st.CostUSD, &st.CostMonthUSD)
	if err != nil {
		return nil, fmt.Errorf("overview totals: %w", err)
	}

	// Per-agent: session counts from the hub, usage from the rollups.
	rows, err := db.QueryContext(ctx, `
		SELECT a.slug,
		       (SELECT COUNT(*) FROM sessions se WHERE se.agent_id = a.id),
		       COALESCE((SELECT MAX(se.modified_at) FROM sessions se WHERE se.agent_id = a.id), ''),
		       COALESCE((SELECT SUM(r.input_tokens + r.output_tokens + r.cache_read_tokens + r.cache_write_tokens)
		                 FROM rollup_usage_daily r WHERE r.agent_id = a.id), 0),
		       COALESCE((SELECT SUM(r.cost_usd) FROM rollup_usage_daily r WHERE r.agent_id = a.id), 0)
		FROM agents a
		ORDER BY 5 DESC, 2 DESC`)
	if err != nil {
		return nil, fmt.Errorf("agent stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AgentStat
		if err := rows.Scan(&a.Agent, &a.Sessions, &a.LastActive, &a.Tokens, &a.CostUSD); err != nil {
			return nil, err
		}
		if a.Sessions > 0 {
			st.Agents = append(st.Agents, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	// Activity: sessions touched per day (26 weeks) + that day's cost.
	rows, err = db.QueryContext(ctx, `
		SELECT act.day, act.n, COALESCE(r.cost, 0)
		FROM (SELECT substr(modified_at, 1, 10) AS day, COUNT(*) AS n
		      FROM sessions
		      WHERE modified_at >= date('now', '-181 days')
		      GROUP BY 1) act
		LEFT JOIN (SELECT day, SUM(cost_usd) AS cost
		           FROM rollup_usage_daily GROUP BY day) r ON r.day = act.day
		ORDER BY act.day`)
	if err != nil {
		return nil, fmt.Errorf("activity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d DayActivity
		if err := rows.Scan(&d.Day, &d.Sessions, &d.CostUSD); err != nil {
			return nil, err
		}
		st.Activity = append(st.Activity, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	rows, err = db.QueryContext(ctx, `
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

	rows, err = db.QueryContext(ctx, `
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
	rows, err = db.QueryContext(ctx, `
		SELECT tc.file_path, tc.kind, a.slug, se.external_id,
		       COALESCE(tc.started_at, se.modified_at, '') AS at
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE tc.file_path <> '' AND tc.kind IN ('file_write', 'file_edit')
		ORDER BY at DESC LIMIT 120`)
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
	Until   string
	Limit   int
	Offset  int
}

// Commands lists shell commands newest-first across every agent.
func (s *Service) Commands(ctx context.Context, f CommandsFilter) ([]CommandRow, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	where := []string{
		`tc.kind = 'shell'`,
		`json_extract(tc.input_json, '$.command') IS NOT NULL`,
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
		where = append(where, `json_extract(tc.input_json, '$.command') LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Query)+"%")
	}
	if f.Since != "" {
		where = append(where, `COALESCE(tc.started_at, se.created_at, '') >= ?`)
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where = append(where, `COALESCE(tc.started_at, se.created_at, '') < ?`)
		args = append(args, f.Until)
	}
	args = append(args, f.Limit, f.Offset)

	rows, err := s.store.DB().QueryContext(ctx, fmt.Sprintf(`
		SELECT json_extract(tc.input_json, '$.command'),
		       COALESCE(tc.started_at, se.created_at, ''),
		       a.slug, se.external_id, se.cwd
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE %s
		ORDER BY COALESCE(tc.started_at, se.created_at, '') DESC, tc.id DESC
		LIMIT ? OFFSET ?`, strings.Join(where, " AND ")), args...)
	if err != nil {
		return nil, fmt.Errorf("listing commands: %w", err)
	}
	defer rows.Close()

	var out []CommandRow
	for rows.Next() {
		var c CommandRow
		if err := rows.Scan(&c.Command, &c.At, &c.Agent, &c.SessionID, &c.CWD); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// editExcerptLimit caps the old/new payloads shipped for diff rendering;
// pathological edits fall back to "diff too large".
const editExcerptLimit = 16 * 1024

// ToolCallRow is one tool invocation of a session, with just enough
// detail to browse (command text or file path, never the full payload) —
// plus the edit payloads for file_edit rows so the UI can render diffs.
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
	// Old/New carry the file_edit before/after excerpts (capped).
	Old string `json:"old,omitempty"`
	New string `json:"new,omitempty"`
}

// SessionTools returns every tool call of one session in order.
func (s *Service) SessionTools(ctx context.Context, agentSlug, externalID string) ([]ToolCallRow, error) {
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.DB().QueryContext(ctx, `
		SELECT tc.seq, tc.message_seq, tc.name, tc.kind,
		       COALESCE(json_extract(tc.input_json, '$.command'), tc.file_path, ''),
		       tc.result_status, COALESCE(tc.started_at, ''),
		       CASE WHEN tc.kind = 'file_edit'
		            THEN substr(COALESCE(json_extract(tc.input_json, '$.old_string'), ''), 1, ?)
		            ELSE '' END,
		       CASE WHEN tc.kind = 'file_edit'
		            THEN substr(COALESCE(json_extract(tc.input_json, '$.new_string'), ''), 1, ?)
		            ELSE '' END
		FROM tool_calls tc WHERE tc.session_id = ? ORDER BY tc.seq`,
		editExcerptLimit, editExcerptLimit, rowID)
	if err != nil {
		return nil, fmt.Errorf("listing tool calls: %w", err)
	}
	defer rows.Close()

	var out []ToolCallRow
	for rows.Next() {
		var t ToolCallRow
		if err := rows.Scan(&t.Seq, &t.MessageSeq, &t.Name, &t.Kind,
			&t.Detail, &t.Status, &t.At, &t.Old, &t.New); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// escapeLike escapes LIKE wildcards so user filters match literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
