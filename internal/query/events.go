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
	canon.EventPRLink, canon.EventTurn, canon.EventCompaction, canon.EventHook,
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
// Sort is "time" (newest first, the default) or "duration" (longest
// first).
type EventsFilter struct {
	Kind    string
	Status  string // comma-separated outcomes (ok, error, blocked, an HTTP status …)
	Agent   string
	Session string
	Since   string
	Until   string
	Sort    string
	Limit   int
	Offset  int
}

var eventOrders = map[string]string{
	"time":     "e.at DESC, e.id DESC",
	"duration": "e.duration_ms DESC, e.id DESC",
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
	if f.Sort == "" {
		f.Sort = "time"
	}
	order, ok := eventOrders[f.Sort]
	if !ok {
		return nil, fmt.Errorf("%w: sort=%q (want time or duration)", ErrBadRequest, f.Sort)
	}
	where, args := eventWhere(kinds, f.Agent, f.Since, f.Until)
	if f.Status != "" {
		statuses := strings.Split(f.Status, ",")
		where = append(where, "e.status IN ("+strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",")+")")
		for _, st := range statuses {
			args = append(args, strings.TrimSpace(st))
		}
	}
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
		ORDER BY `+order+`
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

// EventStat is one (kind, group) aggregate. Failures counts events whose
// status is error or blocked (hooks).
type EventStat struct {
	Kind     string  `json:"kind"`
	Group    string  `json:"group"`
	Count    int     `json:"count"`
	Failures int     `json:"failures"`
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
	"detail": "e.detail",
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
		return nil, fmt.Errorf("%w: group=%q (want label, status, agent, day, or detail)", ErrBadRequest, f.GroupBy)
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
		SELECT e.kind, `+groupExpr+` AS g, COUNT(*), SUM(e.status IN ('error', 'blocked')),
		       COUNT(DISTINCT e.session_id),
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
		if err := rows.Scan(&st.Kind, &st.Group, &st.Count, &st.Failures, &st.Sessions,
			&st.TotalMs, &st.AvgMs, &st.MaxMs); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ToolStatsFilter narrows the tool-stats op. Sort is "errors" (most
// failures first, the default), "total" (most time spent first), or "avg"
// (slowest on average first).
type ToolStatsFilter struct {
	Agent string
	Since string
	Until string
	Sort  string
	Limit int
}

// ToolStat is one tool's call, failure, and timing figures. Timing covers
// only the Timed calls whose duration is known (result timestamps or the
// agent's own figure).
type ToolStat struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Calls    int     `json:"calls"`
	Errors   int     `json:"errors"`
	Sessions int     `json:"sessions"`
	Timed    int     `json:"timed"`
	TotalMs  int64   `json:"totalMs"`
	AvgMs    float64 `json:"avgMs"`
	MaxMs    int64   `json:"maxMs"`
}

var toolOrders = map[string]string{
	"errors": "errors DESC, calls DESC, tc.name",
	"total":  "total_ms DESC, tc.name",
	"avg":    "avg_ms DESC, tc.name",
}

// ToolStats counts calls and failed results per tool, most failures first.
func (s *Service) ToolStats(ctx context.Context, f ToolStatsFilter) ([]ToolStat, error) {
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	if err := ToolStatsLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	if f.Sort == "" {
		f.Sort = "errors"
	}
	order, ok := toolOrders[f.Sort]
	if !ok {
		return nil, fmt.Errorf("%w: sort=%q (want errors, total, or avg)", ErrBadRequest, f.Sort)
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
		SELECT tc.name, tc.kind, COUNT(*) AS calls, SUM(tc.result_status = 'error') AS errors,
		       COUNT(DISTINCT tc.session_id), COUNT(tc.duration_ms),
		       COALESCE(SUM(tc.duration_ms), 0) AS total_ms,
		       COALESCE(AVG(tc.duration_ms), 0) AS avg_ms,
		       COALESCE(MAX(tc.duration_ms), 0)
		FROM tool_calls tc
		JOIN sessions se ON se.id = tc.session_id
		JOIN agents a ON a.id = se.agent_id
		`+whereClause(where)+`
		GROUP BY tc.name, tc.kind
		ORDER BY `+order+`
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("aggregating tool calls: %w", err)
	}
	defer rows.Close()
	var out []ToolStat
	for rows.Next() {
		var st ToolStat
		if err := rows.Scan(&st.Name, &st.Kind, &st.Calls, &st.Errors, &st.Sessions,
			&st.Timed, &st.TotalMs, &st.AvgMs, &st.MaxMs); err != nil {
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

// ContextPoint is one model request: how much context it sent (input plus
// cache reads and writes — the whole prompt) and how much it produced.
type ContextPoint struct {
	Seq          int    `json:"seq"`
	At           string `json:"at,omitempty"`
	PromptTokens int64  `json:"promptTokens"`
	OutputTokens int64  `json:"outputTokens"`
}

// SessionContext is a session's context fill over its requests, with the
// compactions that emptied it.
type SessionContext struct {
	Points      []ContextPoint `json:"points"`
	Compactions []EventRow     `json:"compactions"`
}

// Context returns one session's prompt size per model request, in order,
// with its compactions. Usage owned by another copy of a resumed turn
// (see the usage dedupe) is absent from the copy.
func (s *Service) Context(ctx context.Context, agentSlug, externalID string) (*SessionContext, error) {
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT m.seq, COALESCE(m.created_at, ''),
		       u.input_tokens + u.cache_read_tokens + u.cache_write_tokens, u.output_tokens
		FROM message_usage u JOIN messages m ON m.id = u.message_id
		WHERE m.session_id = ? AND m.role = 'assistant'
		ORDER BY m.seq`, rowID)
	if err != nil {
		return nil, fmt.Errorf("reading context fill: %w", err)
	}
	defer rows.Close()
	out := &SessionContext{Points: []ContextPoint{}}
	for rows.Next() {
		var p ContextPoint
		if err := rows.Scan(&p.Seq, &p.At, &p.PromptTokens, &p.OutputTokens); err != nil {
			return nil, err
		}
		out.Points = append(out.Points, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Compactions, err = s.Events(ctx, EventsFilter{
		Kind: string(canon.EventCompaction), Agent: agentSlug, Session: externalID,
		Limit: EventsLimit.Max,
	})
	if out.Compactions == nil {
		out.Compactions = []EventRow{}
	}
	return out, err
}
