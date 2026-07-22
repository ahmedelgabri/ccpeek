// Package query is the typed query service every surface consumes — web
// UI, `ccpeek query` CLI, /api/v1, and the MCP server (docs/v2-plan.md
// §5.7). One implementation, many transports: anything the UI can show, an
// agent can fetch.
//
// Responses carry locators and data, never presentation URLs; the payload
// schema is versioned by the transports.
package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// ErrNotFound marks lookups for entities that don't exist.
var ErrNotFound = errors.New("not found")

// ErrBadRequest marks caller mistakes (invalid filter values) as opposed
// to internal failures — transports map it to 400 vs 500.
var ErrBadRequest = errors.New("bad request")

// Service executes queries against the store.
type Service struct {
	store  *db.Store
	pricer db.Pricer
}

// New builds a Service.
func New(store *db.Store, pricer db.Pricer) *Service {
	return &Service{store: store, pricer: pricer}
}

// TokenTotals is a usage sum by token type.
type TokenTotals struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

// SessionSummary is one row of the primary `sessions` op.
type SessionSummary struct {
	Agent      string      `json:"agent"`
	ID         string      `json:"id"` // external id
	Title      string      `json:"title"`
	CreatedAt  string      `json:"createdAt"`
	ModifiedAt string      `json:"modifiedAt"`
	CWD        string      `json:"cwd"`
	GitBranch  string      `json:"gitBranch,omitempty"`
	Messages   int         `json:"messages"`
	ToolCalls  int         `json:"toolCalls"`
	Tokens     TokenTotals `json:"tokens"`
	CostUSD    float64     `json:"costUSD"`
	// UnpricedTokens counts tokens whose model the pricing table can't
	// resolve; when non-zero, CostUSD is a lower bound.
	UnpricedTokens int64 `json:"unpricedTokens,omitempty"`
}

// SessionsFilter narrows the sessions op.
type SessionsFilter struct {
	Agent   string // slug, "" = all
	Project string // workspace canonical path, "" = all
	Model   string // sessions with ≥1 message on this model
	Since   string // inclusive YYYY-MM-DD on modified_at
	Until   string // exclusive YYYY-MM-DD upper bound on modified_at
	Query   string // substring on title
	Limit   int
	Offset  int
}

// Sessions lists sessions newest-first — the primary op of the
// session-centric model.
func (s *Service) Sessions(ctx context.Context, f SessionsFilter) ([]SessionSummary, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	var where []string
	var args []any
	if f.Agent != "" {
		where = append(where, `a.slug = ?`)
		args = append(args, f.Agent)
	}
	if f.Project != "" {
		where = append(where, `se.id IN (
			SELECT sw.session_id FROM session_workspaces sw
			JOIN workspaces w ON w.id = sw.workspace_id
			WHERE w.canonical_path = ?)`)
		args = append(args, f.Project)
	}
	if f.Model != "" {
		where = append(where, `se.id IN (
			SELECT DISTINCT m.session_id FROM messages m WHERE m.model = ?)`)
		args = append(args, f.Model)
	}
	if f.Since != "" {
		where = append(where, `se.modified_at >= ?`)
		args = append(args, f.Since)
	}
	if f.Until != "" {
		where = append(where, `se.modified_at < ?`)
		args = append(args, f.Until)
	}
	if f.Query != "" {
		where = append(where, `se.title LIKE ?`)
		args = append(args, "%"+f.Query+"%")
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit, f.Offset)

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT a.slug, se.id, se.external_id, se.title,
		       COALESCE(se.created_at, ''), COALESCE(se.modified_at, ''),
		       se.cwd, se.git_branch,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = se.id),
		       (SELECT COUNT(*) FROM tool_calls tc WHERE tc.session_id = se.id)
		FROM sessions se
		JOIN agents a ON a.id = se.agent_id
		%s
		ORDER BY se.modified_at DESC, se.id DESC
		LIMIT ? OFFSET ?`, clause), args...)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var out []SessionSummary
	var rowIDs []int64
	for rows.Next() {
		var sum SessionSummary
		var rowID int64
		if err := rows.Scan(&sum.Agent, &rowID, &sum.ID, &sum.Title,
			&sum.CreatedAt, &sum.ModifiedAt, &sum.CWD, &sum.GitBranch,
			&sum.Messages, &sum.ToolCalls); err != nil {
			return nil, err
		}
		out = append(out, sum)
		rowIDs = append(rowIDs, rowID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachCosts(ctx, rowIDs, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachCost computes one session's token totals and auto-mode cost.
func (s *Service) attachCost(ctx context.Context, sessionRowID int64, sum *SessionSummary) error {
	return s.attachCosts(ctx, []int64{sessionRowID}, []SessionSummary{*sum},
		func(i int, updated SessionSummary) { *sum = updated })
}

// attachCosts computes token totals and auto-mode cost for a whole page
// of sessions in ONE grouped query — per-row queries made a 100-session
// list cost 101 round trips. apply, when given, receives each updated
// summary (the single-session path writes through a pointer).
func (s *Service) attachCosts(ctx context.Context, rowIDs []int64, sums []SessionSummary, apply ...func(int, SessionSummary)) error {
	if len(rowIDs) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(rowIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(rowIDs))
	byRow := make(map[int64]int, len(rowIDs))
	for i, id := range rowIDs {
		args[i] = id
		byRow[id] = i
	}

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT m.session_id, m.model,
		       SUM(u.input_tokens), SUM(u.output_tokens),
		       SUM(u.cache_read_tokens), SUM(u.cache_write_tokens),
		       SUM(COALESCE(u.reported_cost_usd, 0)),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.input_tokens ELSE 0 END),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.output_tokens ELSE 0 END),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_read_tokens ELSE 0 END),
		       SUM(CASE WHEN u.reported_cost_usd IS NULL THEN u.cache_write_tokens ELSE 0 END)
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		WHERE m.session_id IN (%s)
		GROUP BY m.session_id, m.model`, placeholders), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID int64
		var model string
		var in, out, cr, cw int64
		var reported float64
		var uin, uout, ucr, ucw int64
		if err := rows.Scan(&sessionID, &model, &in, &out, &cr, &cw,
			&reported, &uin, &uout, &ucr, &ucw); err != nil {
			return err
		}
		i, found := byRow[sessionID]
		if !found {
			continue
		}
		sum := &sums[i]
		sum.Tokens.Input += in
		sum.Tokens.Output += out
		sum.Tokens.CacheRead += cr
		sum.Tokens.CacheWrite += cw
		sum.CostUSD += reported
		if uin+uout+ucr+ucw > 0 {
			if rate, ok := s.pricer.Lookup(model); ok {
				sum.CostUSD += rate.Cost(canon.Usage{
					InputTokens: uin, OutputTokens: uout,
					CacheReadTokens: ucr, CacheWriteTokens: ucw,
				})
			} else {
				sum.UnpricedTokens += uin + uout + ucr + ucw
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, fn := range apply {
		for i := range sums {
			fn(i, sums[i])
		}
	}
	return nil
}

// Relation is one session-graph edge, viewed from a session.
type Relation struct {
	Kind      string `json:"kind"`
	Direction string `json:"direction"` // out: this→other, in: other→this
	SessionID string `json:"sessionId"` // the other endpoint's external id
}

// LinkedArtifact is an artifact attached to a session.
type LinkedArtifact struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Relation string `json:"relation"`
	Evidence string `json:"evidence"`
}

// SessionDetail is the `session` op: one session with everything related.
type SessionDetail struct {
	SessionSummary
	Relations []Relation       `json:"relations,omitempty"`
	Artifacts []LinkedArtifact `json:"artifacts,omitempty"`
	Models    []string         `json:"models,omitempty"`
}

// Session returns one session with its relations and linked artifacts.
func (s *Service) Session(ctx context.Context, agentSlug, externalID string) (*SessionDetail, error) {
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}

	detail := &SessionDetail{}
	err = s.store.ReadDB().QueryRowContext(ctx, `
		SELECT a.slug, se.external_id, se.title,
		       COALESCE(se.created_at, ''), COALESCE(se.modified_at, ''),
		       se.cwd, se.git_branch,
		       (SELECT COUNT(*) FROM messages m WHERE m.session_id = se.id),
		       (SELECT COUNT(*) FROM tool_calls tc WHERE tc.session_id = se.id)
		FROM sessions se JOIN agents a ON a.id = se.agent_id
		WHERE se.id = ?`, rowID).
		Scan(&detail.Agent, &detail.ID, &detail.Title, &detail.CreatedAt,
			&detail.ModifiedAt, &detail.CWD, &detail.GitBranch,
			&detail.Messages, &detail.ToolCalls)
	if err != nil {
		return nil, err
	}
	if err := s.attachCost(ctx, rowID, &detail.SessionSummary); err != nil {
		return nil, err
	}

	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT r.kind, 'out', other.external_id
		FROM session_relations r JOIN sessions other ON other.id = r.to_session_id
		WHERE r.from_session_id = ?
		UNION ALL
		SELECT r.kind, 'in', other.external_id
		FROM session_relations r JOIN sessions other ON other.id = r.from_session_id
		WHERE r.to_session_id = ?`, rowID, rowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rel Relation
		if err := rows.Scan(&rel.Kind, &rel.Direction, &rel.SessionID); err != nil {
			return nil, err
		}
		detail.Relations = append(detail.Relations, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	arts, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT ar.kind, ar.name, ass.relation, ass.evidence
		FROM artifact_sessions ass
		JOIN artifacts ar ON ar.id = ass.artifact_id
		WHERE ass.session_id = ?
		ORDER BY ar.kind, ar.name`, rowID)
	if err != nil {
		return nil, err
	}
	defer arts.Close()
	for arts.Next() {
		var la LinkedArtifact
		if err := arts.Scan(&la.Kind, &la.Name, &la.Relation, &la.Evidence); err != nil {
			return nil, err
		}
		detail.Artifacts = append(detail.Artifacts, la)
	}
	if err := arts.Err(); err != nil {
		return nil, err
	}

	models, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT DISTINCT model FROM messages
		WHERE session_id = ? AND model <> '' ORDER BY model`, rowID)
	if err != nil {
		return nil, err
	}
	defer models.Close()
	for models.Next() {
		var m string
		if err := models.Scan(&m); err != nil {
			return nil, err
		}
		detail.Models = append(detail.Models, m)
	}
	return detail, models.Err()
}

// TranscriptMessage is one entry of the `transcript` op. ExternalID and
// ParentID expose the agent's native entry tree (Claude parentUuid, Pi
// id/parentId) so callers can render branches.
type TranscriptMessage struct {
	Seq         int    `json:"seq"`
	ExternalID  string `json:"externalId,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	Role        string `json:"role"`
	Kind        string `json:"kind"`
	CreatedAt   string `json:"createdAt"`
	Model       string `json:"model,omitempty"`
	IsSidechain bool   `json:"isSidechain,omitempty"`
	Text        string `json:"text"`
	// HTML is Text rendered as sanitized markdown; the HTTP layer fills it
	// for the web UI, the CLI/MCP transports leave it empty.
	HTML string `json:"html,omitempty"`
	// Content carries the raw agent payload only when the caller opts in.
	Content string `json:"content,omitempty"`
}

// TranscriptOptions bound the transcript op (token-budget friendly).
type TranscriptOptions struct {
	FromSeq int
	Limit   int
	Full    bool // include raw content payloads
}

// Transcript returns a session's entries in seq order.
func (s *Service) Transcript(ctx context.Context, agentSlug, externalID string, opts TranscriptOptions) ([]TranscriptMessage, error) {
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}
	if opts.Limit <= 0 {
		opts.Limit = 200
	}
	if opts.Limit > 1000 {
		opts.Limit = 1000
	}
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT m.seq, m.external_id, m.parent_external_id,
		       m.role, m.kind, COALESCE(m.created_at, ''), m.model,
		       m.is_sidechain, d.text_content, m.content
		FROM messages m
		LEFT JOIN search_docs d ON d.session_id = m.session_id
			AND d.doc_type = 'message' AND d.seq = m.seq
		WHERE m.session_id = ? AND m.seq >= ?
		ORDER BY m.seq LIMIT ?`, rowID, opts.FromSeq, opts.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TranscriptMessage
	for rows.Next() {
		var tm TranscriptMessage
		var sidechain int
		var text sql.NullString
		var content string
		if err := rows.Scan(&tm.Seq, &tm.ExternalID, &tm.ParentID,
			&tm.Role, &tm.Kind, &tm.CreatedAt,
			&tm.Model, &sidechain, &text, &content); err != nil {
			return nil, err
		}
		tm.IsSidechain = sidechain != 0
		tm.Text = text.String
		if opts.Full {
			tm.Content = content
		}
		out = append(out, tm)
	}
	return out, rows.Err()
}

func (s *Service) sessionRowID(ctx context.Context, agentSlug, externalID string) (int64, error) {
	var id int64
	err := s.store.ReadDB().QueryRowContext(ctx, `
		SELECT se.id FROM sessions se
		JOIN agents a ON a.id = se.agent_id
		WHERE a.slug = ? AND se.external_id = ?`, agentSlug, externalID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: session %s/%s", ErrNotFound, agentSlug, externalID)
	}
	return id, err
}
