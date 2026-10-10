package query

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// eventKinds are the kinds a filter may name; an unknown kind is a caller
// mistake, not an empty answer.
var eventKinds = []canon.EventKind{
	canon.EventAPIError, canon.EventAborted, canon.EventToolDenied,
	canon.EventPRLink,
}

// parseKinds splits a comma-separated kind filter and validates it.
func parseKinds(spec string) ([]string, error) {
	if spec == "" {
		return nil, nil
	}
	var out []string
	for _, k := range strings.Split(spec, ",") {
		k = strings.TrimSpace(k)
		if !slices.Contains(eventKinds, canon.EventKind(k)) {
			return nil, fmt.Errorf("%w: kind=%q (want one of %s)", ErrBadRequest, k, kindList())
		}
		out = append(out, k)
	}
	return out, nil
}

func kindList() string {
	names := make([]string, len(eventKinds))
	for i, k := range eventKinds {
		names[i] = string(k)
	}
	return strings.Join(names, ", ")
}

// eventWhere builds the clauses shared by the event reads.
func eventWhere(kinds []string, agent, since, until string) ([]string, []any) {
	var where []string
	var args []any
	if len(kinds) > 0 {
		where = append(where, "e.kind IN ("+strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")+")")
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	if agent != "" {
		where = append(where, "a.slug = ?")
		args = append(args, agent)
	}
	if since != "" {
		where = append(where, "e.at >= ?")
		args = append(args, since)
	}
	if until != "" {
		where = append(where, "e.at < ?")
		args = append(args, exclusiveUntil(until))
	}
	return where, args
}

func whereClause(where []string) string {
	if len(where) == 0 {
		return ""
	}
	return "WHERE " + strings.Join(where, " AND ")
}

// EventsFilter narrows the session-events op. Kind is a comma-separated
// list; Session (an external id) scopes to one session and needs Agent.
type EventsFilter struct {
	Kind    string
	Agent   string
	Session string
	Since   string
	Until   string
	Limit   int
	Offset  int
}

// EventRow is one session event with the session it belongs to.
type EventRow struct {
	Agent        string          `json:"agent"`
	SessionID    string          `json:"sessionId"`
	SessionTitle string          `json:"sessionTitle"`
	MessageSeq   int             `json:"messageSeq"`
	Kind         string          `json:"kind"`
	At           string          `json:"at,omitempty"`
	Label        string          `json:"label,omitempty"`
	Status       string          `json:"status,omitempty"`
	DurationMs   int64           `json:"durationMs,omitempty"`
	Detail       string          `json:"detail,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
}

// Events lists session events newest first.
func (s *Service) Events(ctx context.Context, f EventsFilter) ([]EventRow, error) {
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	if err := checkOffset(f.Offset); err != nil {
		return nil, err
	}
	if err := EventsLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	kinds, err := parseKinds(f.Kind)
	if err != nil {
		return nil, err
	}
	where, args := eventWhere(kinds, f.Agent, f.Since, f.Until)
	if f.Session != "" {
		if f.Agent == "" {
			return nil, fmt.Errorf("%w: session needs agent (session ids are per agent)", ErrBadRequest)
		}
		rowID, err := s.sessionRowID(ctx, f.Agent, f.Session)
		if err != nil {
			return nil, err
		}
		where = append(where, "e.session_id = ?")
		args = append(args, rowID)
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT a.slug, se.external_id, se.title, e.message_seq, e.kind,
		       COALESCE(e.at, ''), e.label, e.status, e.duration_ms, e.detail, e.payload_json
		FROM session_events e
		JOIN sessions se ON se.id = e.session_id
		JOIN agents a ON a.id = se.agent_id
		`+whereClause(where)+`
		ORDER BY e.at DESC, e.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing session events: %w", err)
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var r EventRow
		var payload string
		if err := rows.Scan(&r.Agent, &r.SessionID, &r.SessionTitle, &r.MessageSeq, &r.Kind,
			&r.At, &r.Label, &r.Status, &r.DurationMs, &r.Detail, &payload); err != nil {
			return nil, err
		}
		if payload != "" && payload != "{}" {
			r.Payload = json.RawMessage(payload)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EventStatsFilter narrows the event-stats op. GroupBy picks the second
// dimension beside kind: label (default), status, agent, or day.
type EventStatsFilter struct {
	Kind    string
	GroupBy string
	Agent   string
	Since   string
	Until   string
	Limit   int
}

// EventStat is one (kind, group) aggregate.
type EventStat struct {
	Kind     string  `json:"kind"`
	Group    string  `json:"group"`
	Count    int     `json:"count"`
	Sessions int     `json:"sessions"`
	TotalMs  int64   `json:"totalMs,omitempty"`
	AvgMs    float64 `json:"avgMs,omitempty"`
	MaxMs    int64   `json:"maxMs,omitempty"`
}

var eventGroupExprs = map[string]string{
	"label":  "e.label",
	"status": "e.status",
	"agent":  "a.slug",
	"day":    "substr(COALESCE(e.at, ''), 1, 10)",
}

// EventStats aggregates session events by kind and one more dimension,
// largest groups first (days in date order).
func (s *Service) EventStats(ctx context.Context, f EventStatsFilter) ([]EventStat, error) {
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	if err := EventStatsLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	if f.GroupBy == "" {
		f.GroupBy = "label"
	}
	groupExpr, ok := eventGroupExprs[f.GroupBy]
	if !ok {
		return nil, fmt.Errorf("%w: group=%q (want label, status, agent, or day)", ErrBadRequest, f.GroupBy)
	}
	kinds, err := parseKinds(f.Kind)
	if err != nil {
		return nil, err
	}
	where, args := eventWhere(kinds, f.Agent, f.Since, f.Until)
	order := "COUNT(*) DESC, e.kind, g"
	if f.GroupBy == "day" {
		order = "g, e.kind"
	}
	limit := ""
	if f.Limit > 0 {
		limit = "LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT e.kind, `+groupExpr+` AS g, COUNT(*), COUNT(DISTINCT e.session_id),
		       SUM(e.duration_ms), AVG(e.duration_ms), MAX(e.duration_ms)
		FROM session_events e
		JOIN sessions se ON se.id = e.session_id
		JOIN agents a ON a.id = se.agent_id
		`+whereClause(where)+`
		GROUP BY e.kind, g
		ORDER BY `+order+` `+limit, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating session events: %w", err)
	}
	defer rows.Close()
	var out []EventStat
	for rows.Next() {
		var st EventStat
		if err := rows.Scan(&st.Kind, &st.Group, &st.Count, &st.Sessions,
			&st.TotalMs, &st.AvgMs, &st.MaxMs); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ToolStatsFilter narrows the tool-stats op.
type ToolStatsFilter struct {
	Agent string
	Since string
	Until string
	Limit int
}

// ToolStat is one tool's call and failure counts.
type ToolStat struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Calls    int    `json:"calls"`
	Errors   int    `json:"errors"`
	Sessions int    `json:"sessions"`
}

// ToolStats counts calls and failed results per tool, most failures first.
func (s *Service) ToolStats(ctx context.Context, f ToolStatsFilter) ([]ToolStat, error) {
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	if err := ToolStatsLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	var where []string
	var args []any
	if f.Agent != "" {
		where = append(where, "a.slug = ?")
		args = append(args, f.Agent)
	}
	if f.Since != "" {
		where = append(where, "tc.started_at >= ?")
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where = append(where, "tc.started_at < ?")
		args = append(args, exclusiveUntil(f.Until))
	}
	args = append(args, f.Limit)
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT tc.name, tc.kind, COUNT(*), SUM(tc.result_status = 'error'),
		       COUNT(DISTINCT tc.session_id)
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		JOIN agents a ON a.id = se.agent_id
		`+whereClause(where)+`
		GROUP BY tc.name, tc.kind
		ORDER BY 4 DESC, 3 DESC, tc.name
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating tool calls: %w", err)
	}
	defer rows.Close()
	var out []ToolStat
	for rows.Next() {
		var st ToolStat
		if err := rows.Scan(&st.Name, &st.Kind, &st.Calls, &st.Errors, &st.Sessions); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// attachEventCounts fills a session detail's per-kind event counts.
func (s *Service) attachEventCounts(ctx context.Context, rowID int64, detail *SessionDetail) error {
	rows, err := s.store.ReadDB().QueryContext(ctx,
		`SELECT kind, COUNT(*) FROM session_events WHERE session_id = ? GROUP BY kind`, rowID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return err
		}
		if detail.Events == nil {
			detail.Events = map[string]int{}
		}
		detail.Events[kind] = n
	}
	return rows.Err()
}
