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
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// exclusiveUntil converts an INCLUSIVE YYYY-MM-DD upper bound into the
// exclusive one the date comparisons use.
//
// "until" is inclusive on every transport. It used to be exclusive in the
// service, and only the web UI knew: it added a day client-side before
// sending. The CLI and MCP had no such helper, so the same nominal
// argument meant different ranges depending on who sent it, and an agent
// asking for "usage from the 1st to the 25th" silently lost the 25th.
func exclusiveUntil(until string) string {
	if until == "" {
		return ""
	}
	d, err := time.Parse("2006-01-02", until)
	if err != nil {
		// Transports validate the format; anything else passes through and
		// simply matches nothing rather than widening the range.
		return until
	}
	return d.AddDate(0, 0, 1).Format("2006-01-02")
}

// ErrNotFound marks lookups for entities that don't exist.
var ErrNotFound = errors.New("not found")

// ErrBadRequest marks caller mistakes (invalid filter values) as opposed
// to internal failures — transports map it to 400 vs 500.
var ErrBadRequest = errors.New("bad request")

func badRequest(detail string) error { return fmt.Errorf("%w: %s", ErrBadRequest, detail) }

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
	Agent                 string      `json:"agent"`
	ID                    string      `json:"id"` // external id
	Title                 string      `json:"title"`
	CreatedAt             string      `json:"createdAt"`
	ModifiedAt            string      `json:"modifiedAt"`
	CWD                   string      `json:"cwd"`
	GitBranch             string      `json:"gitBranch,omitempty"`
	Messages              int         `json:"messages"`
	ToolCalls             int         `json:"toolCalls"`
	Tokens                TokenTotals `json:"tokens"`
	CostUSD               float64     `json:"costUSD"`
	CostUSDExact          string      `json:"costUSDExact"`
	CostReportedUSD       float64     `json:"costReportedUSD"`
	CostReportedUSDExact  string      `json:"costReportedUSDExact"`
	CostEstimatedUSD      float64     `json:"costEstimatedUSD"`
	CostEstimatedUSDExact string      `json:"costEstimatedUSDExact"`
	// UnpricedTokens counts tokens whose model the pricing table can't
	// resolve; when non-zero, CostUSD is a lower bound.
	UnpricedTokens     int64        `json:"unpricedTokens,omitempty"`
	UnpricedTokenTypes *TokenTotals `json:"unpricedTokenTypes,omitempty"`
	// ParentID is set on a subagent's session: the session (or parent
	// subagent) that spawned it. SubagentType is the spawning agent type
	// (Explore, general-purpose, …). SubagentCount counts direct children.
	ParentID      string `json:"parentId,omitempty"`
	SubagentType  string `json:"subagentType,omitempty"`
	SubagentCount int    `json:"subagentCount,omitempty"`
}

// summaryColumns selects one SessionSummary row from `sessions se JOIN
// agents a`; scanSummary reads it. The session list, the session detail,
// and the detail's subagent list all read the same shape.
const summaryColumns = `a.slug, se.id, se.external_id, se.title,
	COALESCE(se.created_at, ''), COALESCE(se.modified_at, ''),
	se.cwd, se.git_branch,
	(SELECT COUNT(*) FROM messages m WHERE m.session_id = se.id),
	(SELECT COUNT(*) FROM tool_calls tc WHERE tc.session_id = se.id),
	COALESCE((SELECT p.external_id FROM session_relations r JOIN sessions p ON p.id = r.to_session_id
	          WHERE r.from_session_id = se.id AND r.kind = 'subagent_of'), ''),
	COALESCE((SELECT json_extract(r.evidence_json, '$.agentType') FROM session_relations r
	          WHERE r.from_session_id = se.id AND r.kind = 'subagent_of'), ''),
	(SELECT COUNT(*) FROM session_relations r WHERE r.to_session_id = se.id AND r.kind = 'subagent_of')`

func scanSummary(rows interface{ Scan(...any) error }, sum *SessionSummary, rowID *int64) error {
	return rows.Scan(&sum.Agent, rowID, &sum.ID, &sum.Title,
		&sum.CreatedAt, &sum.ModifiedAt, &sum.CWD, &sum.GitBranch,
		&sum.Messages, &sum.ToolCalls,
		&sum.ParentID, &sum.SubagentType, &sum.SubagentCount)
}

// SessionsFilter narrows the sessions op.
type SessionsFilter struct {
	Agent   string // slug, "" = all
	Project string // workspace canonical path, "" = all
	Model   string // sessions with ≥1 message on this model
	Since   string // inclusive YYYY-MM-DD on modified_at
	Until   string // INCLUSIVE YYYY-MM-DD upper bound on modified_at
	Query   string // substring on title
	// Subagents includes subagent sessions, which are otherwise hidden: a
	// parent can spawn dozens, and they read as noise beside the
	// conversations a person actually had. The parent's detail lists them.
	Subagents bool
	Limit     int
	Offset    int
}

// Sessions lists sessions newest-first — the primary op of the
// session-centric model.
func (s *Service) Sessions(ctx context.Context, f SessionsFilter) ([]SessionSummary, error) {
	if err := checkWindow(f.Since, f.Until); err != nil {
		return nil, err
	}
	if err := checkOffset(f.Offset); err != nil {
		return nil, err
	}
	if err := SessionsLimit.apply(&f.Limit); err != nil {
		return nil, err
	}
	var where []string
	var args []any
	if f.Agent != "" {
		where = append(where, `a.slug = ?`)
		args = append(args, f.Agent)
	}
	if f.Project != "" {
		// The column holds a canonical path, so the caller's must be
		// canonicalized to match it — otherwise ?project=/home/u/proj/
		// finds nothing while ?project=/home/u/proj finds everything.
		where = append(where, `se.id IN (
			SELECT sw.session_id FROM session_workspaces sw
			JOIN workspaces w ON w.id = sw.workspace_id
			WHERE w.canonical_path = ?)`)
		args = append(args, db.CanonicalWorkspace(f.Project))
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
		args = append(args, exclusiveUntil(f.Until))
	}
	if f.Query != "" {
		// Escaped like every other LIKE filter in this package: a "%" or
		// "_" the user types is a literal, not a wildcard.
		where = append(where, `se.title LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Query)+"%")
	}
	if !f.Subagents {
		where = append(where, `NOT EXISTS (SELECT 1 FROM session_relations r
			WHERE r.from_session_id = se.id AND r.kind = 'subagent_of')`)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit, f.Offset)

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT %s
		FROM sessions se
		JOIN agents a ON a.id = se.agent_id
		%s
		ORDER BY se.modified_at DESC, se.id DESC
		LIMIT ? OFFSET ?`, summaryColumns, clause), args...)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}

	return s.summaries(ctx, rows)
}

// summaries scans summaryColumns rows and attaches their costs.
func (s *Service) summaries(ctx context.Context, rows *sql.Rows) ([]SessionSummary, error) {
	defer rows.Close()
	var out []SessionSummary
	var rowIDs []int64
	for rows.Next() {
		var sum SessionSummary
		var rowID int64
		if err := scanSummary(rows, &sum, &rowID); err != nil {
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
	one := []SessionSummary{*sum}
	if err := s.attachCosts(ctx, []int64{sessionRowID}, one); err != nil {
		return err
	}
	*sum = one[0]
	return nil
}

// attachCosts computes token totals and request-scoped cost for a whole page
// in one query. Rows stay independent so historical cards and context tiers
// remain correct.
func (s *Service) attachCosts(ctx context.Context, rowIDs []int64, sums []SessionSummary) error {
	byRow := make(map[int64]int, len(rowIDs))
	for i, id := range rowIDs {
		byRow[id] = i
	}
	return s.accumulateCosts(ctx, byRow, sums)
}

// accumulateCosts adds each listed session's usage and cost into the
// summary slot byRow assigns it. Several sessions may share a slot, which
// is how a parent's subagents are totalled as one figure.
func (s *Service) accumulateCosts(ctx context.Context, byRow map[int64]int, sums []SessionSummary) error {
	if len(byRow) == 0 {
		return nil
	}
	placeholders := strings.Repeat("?,", len(byRow))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, len(byRow))
	for id := range byRow {
		args = append(args, id)
	}

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT m.session_id, m.provider, m.model,
		       COALESCE(m.created_at, se.created_at, ''),
		       u.input_tokens, u.output_tokens, u.cache_read_tokens,
		       u.cache_write_tokens, u.cache_write_1h_tokens,
		       `+db.ReportedCostNanosExpr+`
		FROM message_usage u
		JOIN messages m ON m.id = u.message_id
		JOIN sessions se ON se.id = m.session_id
		WHERE m.session_id IN (%s)
		ORDER BY m.id`, placeholders), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	amounts := make([]pricing.Amount, len(sums))
	reportedAmounts := make([]pricing.Amount, len(sums))
	estimatedAmounts := make([]pricing.Amount, len(sums))
	for rows.Next() {
		var sessionID int64
		var provider, model, occurredAt string
		var usage canon.Usage
		var reported sql.NullInt64
		if err := rows.Scan(&sessionID, &provider, &model, &occurredAt,
			&usage.InputTokens, &usage.OutputTokens, &usage.CacheReadTokens,
			&usage.CacheWriteTokens, &usage.CacheWrite1hTokens, &reported); err != nil {
			return err
		}
		i, found := byRow[sessionID]
		if !found {
			continue
		}
		sum := &sums[i]
		sum.Tokens.Input += usage.InputTokens
		sum.Tokens.Output += usage.OutputTokens
		sum.Tokens.CacheRead += usage.CacheReadTokens
		sum.Tokens.CacheWrite += usage.CacheWriteTokens
		var reportedAmount *pricing.Amount
		if reported.Valid {
			amount := pricing.Amount(reported.Int64)
			reportedAmount = &amount
		}
		result, err := db.EvaluateCostAt(s.pricer, provider, model,
			db.ParseCostTime(occurredAt), usage, reportedAmount)
		if err != nil {
			return err
		}
		if amounts[i], err = amounts[i].Add(result.Amount); err != nil {
			return err
		}
		if reportedAmounts[i], err = reportedAmounts[i].Add(result.Reported); err != nil {
			return err
		}
		if estimatedAmounts[i], err = estimatedAmounts[i].Add(result.Estimated); err != nil {
			return err
		}
		addUnpriced(&sum.UnpricedTokens, &sum.UnpricedTokenTypes, result.Unpriced)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range sums {
		sums[i].CostUSD = amounts[i].USD()
		sums[i].CostUSDExact = amounts[i].String()
		sums[i].CostReportedUSD = reportedAmounts[i].USD()
		sums[i].CostReportedUSDExact = reportedAmounts[i].String()
		sums[i].CostEstimatedUSD = estimatedAmounts[i].USD()
		sums[i].CostEstimatedUSDExact = estimatedAmounts[i].String()
	}
	return nil
}

func addUnpriced(total *int64, breakdown **TokenTotals, u canon.Usage) {
	n := max(u.InputTokens, 0) + max(u.OutputTokens, 0) +
		max(u.CacheReadTokens, 0) + max(u.CacheWriteTokens, 0)
	if n == 0 {
		return
	}
	*total += n
	if *breakdown == nil {
		*breakdown = &TokenTotals{}
	}
	(*breakdown).Input += max(u.InputTokens, 0)
	(*breakdown).Output += max(u.OutputTokens, 0)
	(*breakdown).CacheRead += max(u.CacheReadTokens, 0)
	(*breakdown).CacheWrite += max(u.CacheWriteTokens, 0)
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
	// Subagents lists the sessions this one spawned directly, oldest first.
	// SubagentTotal sums every descendant (subagents of subagents too):
	// the session's own cost covers only its own turns.
	Subagents     []SessionSummary `json:"subagents,omitempty"`
	SubagentTotal *SubagentTotal   `json:"subagentTotal,omitempty"`
}

// SubagentTotal is the usage and cost of all of a session's descendant
// subagent sessions taken together.
type SubagentTotal struct {
	Sessions       int         `json:"sessions"`
	Messages       int         `json:"messages"`
	Tokens         TokenTotals `json:"tokens"`
	CostUSD        float64     `json:"costUSD"`
	CostUSDExact   string      `json:"costUSDExact"`
	UnpricedTokens int64       `json:"unpricedTokens,omitempty"`
}

// Session returns one session with its relations and linked artifacts.
func (s *Service) Session(ctx context.Context, agentSlug, externalID string) (*SessionDetail, error) {
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}

	detail := &SessionDetail{}
	var ignored int64
	err = scanSummary(s.store.ReadDB().QueryRowContext(ctx, `
		SELECT `+summaryColumns+`
		FROM sessions se JOIN agents a ON a.id = se.agent_id
		WHERE se.id = ?`, rowID), &detail.SessionSummary, &ignored)
	if err != nil {
		return nil, err
	}
	if err := s.attachCost(ctx, rowID, &detail.SessionSummary); err != nil {
		return nil, err
	}
	if detail.SubagentCount > 0 {
		if err := s.attachSubagents(ctx, rowID, detail); err != nil {
			return nil, err
		}
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
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	IsSidechain bool   `json:"isSidechain,omitempty"`
	Text        string `json:"text"`
	// HTML is Text rendered as sanitized markdown; the HTTP layer fills it
	// for the web UI, the CLI/MCP transports leave it empty.
	HTML string `json:"html,omitempty"`
	// Content carries the raw agent payload only when the caller opts in.
	Content string `json:"content,omitempty"`
	// Thinking is the model's reasoning text, only when the caller opts in:
	// it is often longer than the reply itself.
	Thinking string `json:"thinking,omitempty"`
	// Images lists the entry's images; fetch one's bytes with MessageImage
	// (HTTP: .../transcript/{seq}/images/{index}).
	Images []TranscriptImage `json:"images,omitempty"`
}

// TranscriptImage describes one image of a transcript entry.
type TranscriptImage struct {
	Index     int    `json:"index"`
	MediaType string `json:"mediaType"`
}

// TranscriptOptions bound the transcript op (token-budget friendly).
type TranscriptOptions struct {
	FromSeq int
	Limit   int
	Full    bool // include raw content payloads
	// Thinking includes reasoning text. Off by default for the same reason
	// raw payloads are: token budget.
	Thinking bool
}

// Transcript returns a session's entries in seq order.
func (s *Service) Transcript(ctx context.Context, agentSlug, externalID string, opts TranscriptOptions) ([]TranscriptMessage, error) {
	// The bounds are checked before the session is looked up: a malformed
	// one is a caller mistake whether or not the session exists. The
	// transcript pages by seq rather than by offset, so there is no offset
	// to check — a negative from_seq is its own check, and used to pass
	// through to `m.seq >= -5`, returning the WHOLE transcript with a
	// success status on the CLI and MCP while HTTP answered 400.
	if err := TranscriptLimit.apply(&opts.Limit); err != nil {
		return nil, err
	}
	if err := checkSeq("from_seq", opts.FromSeq); err != nil {
		return nil, err
	}
	rowID, err := s.sessionRowID(ctx, agentSlug, externalID)
	if err != nil {
		return nil, err
	}
	contentExpr := "''"
	if opts.Full {
		contentExpr = "m.content"
	}
	thinkingExpr := "''"
	if opts.Thinking {
		thinkingExpr = "m.thinking_text"
	}
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT m.seq, m.external_id, m.parent_external_id,
		       m.role, m.kind, COALESCE(m.created_at, ''), m.provider, m.model,
		       m.is_sidechain, m.text_content, `+contentExpr+`, `+thinkingExpr+`, m.images_json
		FROM messages m
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
		var content, images string
		if err := rows.Scan(&tm.Seq, &tm.ExternalID, &tm.ParentID,
			&tm.Role, &tm.Kind, &tm.CreatedAt, &tm.Provider,
			&tm.Model, &sidechain, &text, &content, &tm.Thinking, &images); err != nil {
			return nil, err
		}
		for i, ref := range imageRefs(images) {
			tm.Images = append(tm.Images, TranscriptImage{Index: i, MediaType: ref.MediaType})
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

// attachSubagents lists a session's direct subagents and totals all of
// its descendants.
func (s *Service) attachSubagents(ctx context.Context, rowID int64, detail *SessionDetail) error {
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT `+summaryColumns+`
		FROM session_relations rel
		JOIN sessions se ON se.id = rel.from_session_id
		JOIN agents a ON a.id = se.agent_id
		WHERE rel.to_session_id = ? AND rel.kind = 'subagent_of'
		ORDER BY se.created_at, se.id`, rowID)
	if err != nil {
		return err
	}
	if detail.Subagents, err = s.summaries(ctx, rows); err != nil {
		return err
	}

	descendants, err := s.store.ReadDB().QueryContext(ctx, `
		WITH RECURSIVE d(id) AS (
			SELECT from_session_id FROM session_relations
			WHERE to_session_id = ? AND kind = 'subagent_of'
			UNION
			SELECT r.from_session_id FROM session_relations r JOIN d ON r.to_session_id = d.id
			WHERE r.kind = 'subagent_of'
		)
		SELECT d.id, (SELECT COUNT(*) FROM messages m WHERE m.session_id = d.id) FROM d`, rowID)
	if err != nil {
		return err
	}
	defer descendants.Close()
	total := &SubagentTotal{}
	slots := map[int64]int{}
	for descendants.Next() {
		var id int64
		var messages int
		if err := descendants.Scan(&id, &messages); err != nil {
			return err
		}
		slots[id] = 0
		total.Sessions++
		total.Messages += messages
	}
	if err := descendants.Err(); err != nil {
		return err
	}
	sum := []SessionSummary{{}}
	if err := s.accumulateCosts(ctx, slots, sum); err != nil {
		return err
	}
	total.Tokens = sum[0].Tokens
	total.CostUSD = sum[0].CostUSD
	total.CostUSDExact = sum[0].CostUSDExact
	total.UnpricedTokens = sum[0].UnpricedTokens
	detail.SubagentTotal = total
	return nil
}
