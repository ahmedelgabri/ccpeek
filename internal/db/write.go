package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// Writer applies canonical records inside a single transaction — the
// pipeline opens one Writer per changed source file (docs/v2-plan.md §5.6).
// It resolves natural keys ((agent, external_id) for sessions,
// (agent, kind, name) for artifacts) to row ids, caching within the
// transaction, and routes links whose target session isn't ingested yet to
// the pending tables for end-of-run resolution.
type Writer struct {
	tx       *sql.Tx
	ctx      context.Context
	agents   map[canon.AgentSlug]int64
	sessions map[sessionKey]int64
}

type sessionKey struct {
	agent      canon.AgentSlug
	externalID string
}

// ErrUnknownSession is returned by writes that require an already-ingested
// session row (messages, tool calls) when none exists.
var ErrUnknownSession = errors.New("session not ingested")

// BeginWrite starts a write transaction.
func (s *Store) BeginWrite(ctx context.Context) (*Writer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Writer{
		tx:       tx,
		ctx:      ctx,
		agents:   make(map[canon.AgentSlug]int64),
		sessions: make(map[sessionKey]int64),
	}, nil
}

// Commit commits the transaction.
func (w *Writer) Commit() error { return w.tx.Commit() }

// Rollback aborts the transaction; safe to defer after Commit.
func (w *Writer) Rollback() error {
	err := w.tx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

// EnsureAgent returns the row id for slug, creating it if needed.
func (w *Writer) EnsureAgent(slug canon.AgentSlug) (int64, error) {
	if id, ok := w.agents[slug]; ok {
		return id, nil
	}
	if _, err := w.tx.ExecContext(w.ctx,
		`INSERT INTO agents (slug) VALUES (?) ON CONFLICT(slug) DO NOTHING`, string(slug)); err != nil {
		return 0, fmt.Errorf("ensuring agent %s: %w", slug, err)
	}
	var id int64
	if err := w.tx.QueryRowContext(w.ctx,
		`SELECT id FROM agents WHERE slug = ?`, string(slug)).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving agent %s: %w", slug, err)
	}
	w.agents[slug] = id
	return id, nil
}

// UpsertSession inserts or updates a session by natural key and returns its
// row id.
func (w *Writer) UpsertSession(sess canon.Session, contentHash string) (int64, error) {
	agentID, err := w.EnsureAgent(sess.Agent)
	if err != nil {
		return 0, err
	}
	if sess.Origin == "" {
		sess.Origin = canon.OriginIngest
	}
	_, err = w.tx.ExecContext(w.ctx, `
		INSERT INTO sessions
			(agent_id, external_id, title, created_at, modified_at,
			 cwd, repo_root, git_branch, origin, source_path, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, external_id) DO UPDATE SET
			title = excluded.title,
			created_at = excluded.created_at,
			modified_at = excluded.modified_at,
			cwd = excluded.cwd,
			repo_root = excluded.repo_root,
			git_branch = excluded.git_branch,
			origin = excluded.origin,
			source_path = excluded.source_path,
			content_hash = excluded.content_hash`,
		agentID, sess.ExternalID, sess.Title,
		timeText(sess.CreatedAt), timeText(sess.ModifiedAt),
		sess.CWD, sess.RepoRoot, sess.GitBranch,
		string(sess.Origin), sess.SourcePath, contentHash)
	if err != nil {
		return 0, fmt.Errorf("upserting session %s/%s: %w", sess.Agent, sess.ExternalID, err)
	}
	id, err := w.sessionID(sess.Agent, sess.ExternalID)
	if err != nil {
		return 0, err
	}
	return id, nil
}

// ClearSessionChildren removes messages, tool calls, and search documents
// for a session prior to re-inserting them when its source changed.
func (w *Writer) ClearSessionChildren(sessionID int64) error {
	for _, q := range []string{
		`DELETE FROM search_docs WHERE session_id = ?`,
		`DELETE FROM messages WHERE session_id = ?`,
		`DELETE FROM tool_calls WHERE session_id = ?`,
	} {
		if _, err := w.tx.ExecContext(w.ctx, q, sessionID); err != nil {
			return err
		}
	}
	return nil
}

// InsertSearchDoc adds one document to the search index. Exactly one of
// sessionID/artifactID should be non-zero; seq anchors message docs within
// their session. Presentation URLs are built by the query layer from these
// locators.
func (w *Writer) InsertSearchDoc(sessionID, artifactID int64, docType string, seq int, title, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var sid, aid any
	if sessionID != 0 {
		sid = sessionID
	}
	if artifactID != 0 {
		aid = artifactID
	}
	_, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO search_docs (session_id, artifact_id, doc_type, seq, title, text_content)
		VALUES (?, ?, ?, ?, ?, ?)`,
		sid, aid, docType, seq, title, text)
	if err != nil {
		return fmt.Errorf("inserting search doc (%s): %w", docType, err)
	}
	return nil
}

// ClearArtifactSearchDocs removes an artifact's search documents before
// re-indexing changed content.
func (w *Writer) ClearArtifactSearchDocs(artifactID int64) error {
	_, err := w.tx.ExecContext(w.ctx,
		`DELETE FROM search_docs WHERE artifact_id = ?`, artifactID)
	return err
}

// InsertMessage writes one message (and its usage, when present) for an
// already-upserted session. Usage rows are deduped agent-wide by
// (content_id, request_id): one assistant turn spans several JSONL lines
// in Claude Code, and resumed/forked session files repeat earlier entries —
// counting either twice would inflate cost (docs/v2-plan.md §5.3). The
// message row itself is always written so transcripts stay complete.
func (w *Writer) InsertMessage(sessionID int64, agent canon.AgentSlug, msg canon.Message) error {
	res, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO messages
			(session_id, seq, external_id, parent_external_id, content_id,
			 role, kind, created_at, model, cwd, is_sidechain, content)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, msg.Seq, msg.ExternalID, msg.ParentExternalID, msg.ContentID,
		string(msg.Role), kindText(msg.Kind), timeText(msg.CreatedAt),
		msg.Model, msg.CWD, boolInt(msg.IsSidechain), string(msg.Content))
	if err != nil {
		return fmt.Errorf("inserting message seq %d: %w", msg.Seq, err)
	}
	if msg.Usage == nil {
		return nil
	}

	if msg.ContentID != "" && msg.Usage.RequestID != "" {
		agentID, err := w.EnsureAgent(agent)
		if err != nil {
			return err
		}
		var dup int
		err = w.tx.QueryRowContext(w.ctx, `
			SELECT 1 FROM message_usage u
			JOIN messages m ON m.id = u.message_id
			JOIN sessions s ON s.id = m.session_id
			WHERE s.agent_id = ? AND m.content_id = ? AND u.request_id = ?
			LIMIT 1`,
			agentID, msg.ContentID, msg.Usage.RequestID).Scan(&dup)
		switch {
		case err == nil:
			return nil // duplicate usage: transcript kept, tokens not double-counted
		case errors.Is(err, sql.ErrNoRows):
			// first sighting — fall through and record it
		default:
			return fmt.Errorf("usage dedupe lookup: %w", err)
		}
	}

	msgID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("message rowid: %w", err)
	}
	u := msg.Usage
	if _, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO message_usage
			(message_id, input_tokens, output_tokens, cache_read_tokens,
			 cache_write_tokens, reasoning_tokens, service_tier,
			 reported_cost_usd, request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msgID, u.InputTokens, u.OutputTokens, u.CacheReadTokens,
		u.CacheWriteTokens, u.ReasoningTokens, u.ServiceTier,
		u.ReportedCostUSD, u.RequestID); err != nil {
		return fmt.Errorf("inserting usage for message seq %d: %w", msg.Seq, err)
	}
	return nil
}

// InsertToolCall writes one tool call for an already-upserted session.
func (w *Writer) InsertToolCall(sessionID int64, tc canon.ToolCall) error {
	kind := tc.Kind
	if kind == "" {
		kind = canon.ToolOther
	}
	input := string(tc.Input)
	if input == "" {
		input = "{}"
	}
	_, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO tool_calls
			(session_id, message_seq, seq, name, kind, input_json,
			 result_status, result_excerpt, file_path, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, tc.MessageSeq, tc.Seq, tc.Name, string(kind), input,
		tc.ResultStatus, tc.ResultExcerpt, tc.FilePath, timeText(tc.StartedAt))
	if err != nil {
		return fmt.Errorf("inserting tool call seq %d (%s): %w", tc.Seq, tc.Name, err)
	}
	return nil
}

// UpsertArtifact inserts or updates an artifact by natural key and returns
// its row id.
func (w *Writer) UpsertArtifact(a canon.Artifact, contentHash string) (int64, error) {
	agentID, err := w.EnsureAgent(a.Agent)
	if err != nil {
		return 0, err
	}
	metadata := string(a.Metadata)
	if metadata == "" {
		metadata = "{}"
	}
	if _, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO artifacts
			(agent_id, kind, name, content, metadata_json, source_path, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, kind, name) DO UPDATE SET
			content = excluded.content,
			metadata_json = excluded.metadata_json,
			source_path = excluded.source_path,
			content_hash = excluded.content_hash`,
		agentID, string(a.Kind), a.Name, a.Content, metadata,
		a.SourcePath, contentHash); err != nil {
		return 0, fmt.Errorf("upserting artifact %s/%s: %w", a.Kind, a.Name, err)
	}
	var id int64
	if err := w.tx.QueryRowContext(w.ctx,
		`SELECT id FROM artifacts WHERE agent_id = ? AND kind = ? AND name = ?`,
		agentID, string(a.Kind), a.Name).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving artifact %s/%s: %w", a.Kind, a.Name, err)
	}
	return id, nil
}

// LinkArtifact connects an artifact to a session. When the session isn't
// ingested yet the link is parked in pending_artifact_links; it reports
// whether the link resolved immediately.
func (w *Writer) LinkArtifact(artifactID int64, link canon.ArtifactLink) (resolved bool, err error) {
	sessionID, err := w.sessionID(link.Agent, link.SessionExternalID)
	switch {
	case err == nil:
		_, err = w.tx.ExecContext(w.ctx, `
			INSERT INTO artifact_sessions (artifact_id, session_id, relation, evidence)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(artifact_id, session_id, relation) DO UPDATE SET
				evidence = excluded.evidence`,
			artifactID, sessionID, string(link.Relation), string(link.Evidence))
		if err != nil {
			return false, fmt.Errorf("linking artifact %d: %w", artifactID, err)
		}
		return true, nil

	case errors.Is(err, ErrUnknownSession):
		agentID, aerr := w.EnsureAgent(link.Agent)
		if aerr != nil {
			return false, aerr
		}
		_, err = w.tx.ExecContext(w.ctx, `
			INSERT INTO pending_artifact_links
				(artifact_id, agent_id, session_external_id, relation, evidence)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(artifact_id, session_external_id, relation) DO UPDATE SET
				evidence = excluded.evidence`,
			artifactID, agentID, link.SessionExternalID,
			string(link.Relation), string(link.Evidence))
		if err != nil {
			return false, fmt.Errorf("parking artifact link %d: %w", artifactID, err)
		}
		return false, nil

	default:
		return false, err
	}
}

// AddSessionRelation records a session→session edge, parking it when either
// endpoint is missing; it reports whether the edge resolved immediately.
func (w *Writer) AddSessionRelation(rel canon.SessionRelation) (resolved bool, err error) {
	evidence := string(rel.Evidence)
	if evidence == "" {
		evidence = "{}"
	}
	fromID, errFrom := w.sessionID(rel.Agent, rel.FromExternalID)
	toID, errTo := w.sessionID(rel.Agent, rel.ToExternalID)
	if errFrom == nil && errTo == nil {
		_, err := w.tx.ExecContext(w.ctx, `
			INSERT INTO session_relations (from_session_id, to_session_id, kind, evidence_json)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(from_session_id, to_session_id, kind) DO UPDATE SET
				evidence_json = excluded.evidence_json`,
			fromID, toID, string(rel.Kind), evidence)
		if err != nil {
			return false, fmt.Errorf("adding relation %s: %w", rel.Kind, err)
		}
		return true, nil
	}
	for _, e := range []error{errFrom, errTo} {
		if e != nil && !errors.Is(e, ErrUnknownSession) {
			return false, e
		}
	}
	agentID, err := w.EnsureAgent(rel.Agent)
	if err != nil {
		return false, err
	}
	if _, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO pending_relations
			(agent_id, from_external_id, to_external_id, kind, evidence_json)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(agent_id, from_external_id, to_external_id, kind) DO UPDATE SET
			evidence_json = excluded.evidence_json`,
		agentID, rel.FromExternalID, rel.ToExternalID,
		string(rel.Kind), evidence); err != nil {
		return false, fmt.Errorf("parking relation %s: %w", rel.Kind, err)
	}
	return false, nil
}

// InsertHistory writes one prompt-history entry, resolving its session link
// when possible.
func (w *Writer) InsertHistory(h canon.HistoryEntry) error {
	agentID, err := w.EnsureAgent(h.Agent)
	if err != nil {
		return err
	}
	var sessionID any
	if h.SessionExternalID != "" {
		if id, err := w.sessionID(h.Agent, h.SessionExternalID); err == nil {
			sessionID = id
		} else if !errors.Is(err, ErrUnknownSession) {
			return err
		}
	}
	_, err = w.tx.ExecContext(w.ctx, `
		INSERT INTO history (agent_id, display, timestamp, session_id)
		VALUES (?, ?, ?, ?)`,
		agentID, h.Display, h.Timestamp.UnixMilli(), sessionID)
	if err != nil {
		return fmt.Errorf("inserting history entry: %w", err)
	}
	return nil
}

// RecordSourceFile stores the content hash for incremental comparison.
func (w *Writer) RecordSourceFile(path string, agent canon.AgentSlug, contentHash string) error {
	agentID, err := w.EnsureAgent(agent)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `
		INSERT INTO source_files (path, agent_id, content_hash, indexed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			agent_id = excluded.agent_id,
			content_hash = excluded.content_hash,
			indexed_at = excluded.indexed_at`,
		path, agentID, contentHash, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (w *Writer) sessionID(agent canon.AgentSlug, externalID string) (int64, error) {
	if externalID == "" {
		return 0, ErrUnknownSession
	}
	key := sessionKey{agent, externalID}
	if id, ok := w.sessions[key]; ok {
		return id, nil
	}
	agentID, err := w.EnsureAgent(agent)
	if err != nil {
		return 0, err
	}
	var id int64
	err = w.tx.QueryRowContext(w.ctx,
		`SELECT id FROM sessions WHERE agent_id = ? AND external_id = ?`,
		agentID, externalID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s/%s", ErrUnknownSession, agent, externalID)
	}
	if err != nil {
		return 0, err
	}
	w.sessions[key] = id
	return id, nil
}

// ResolvePending links parked relations and artifact links whose endpoint
// sessions have since been ingested. Called once at the end of each ingest
// run; rows that still don't resolve stay parked and are counted as
// unresolved_links in run telemetry.
func (s *Store) ResolvePending(ctx context.Context) (resolved, remaining int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO session_relations (from_session_id, to_session_id, kind, evidence_json)
		SELECT sf.id, st.id, p.kind, p.evidence_json
		FROM pending_relations p
		JOIN sessions sf ON sf.agent_id = p.agent_id AND sf.external_id = p.from_external_id
		JOIN sessions st ON st.agent_id = p.agent_id AND st.external_id = p.to_external_id
		WHERE TRUE
		ON CONFLICT(from_session_id, to_session_id, kind) DO NOTHING`)
	if err != nil {
		return 0, 0, fmt.Errorf("resolving pending relations: %w", err)
	}
	nRel, _ := res.RowsAffected()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM pending_relations WHERE EXISTS (
			SELECT 1 FROM sessions sf, sessions st
			WHERE sf.agent_id = pending_relations.agent_id
			  AND sf.external_id = pending_relations.from_external_id
			  AND st.agent_id = pending_relations.agent_id
			  AND st.external_id = pending_relations.to_external_id)`); err != nil {
		return 0, 0, fmt.Errorf("pruning resolved relations: %w", err)
	}

	res, err = tx.ExecContext(ctx, `
		INSERT INTO artifact_sessions (artifact_id, session_id, relation, evidence)
		SELECT p.artifact_id, s.id, p.relation, p.evidence
		FROM pending_artifact_links p
		JOIN sessions s ON s.agent_id = p.agent_id AND s.external_id = p.session_external_id
		WHERE TRUE
		ON CONFLICT(artifact_id, session_id, relation) DO NOTHING`)
	if err != nil {
		return 0, 0, fmt.Errorf("resolving pending artifact links: %w", err)
	}
	nArt, _ := res.RowsAffected()

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM pending_artifact_links WHERE EXISTS (
			SELECT 1 FROM sessions s
			WHERE s.agent_id = pending_artifact_links.agent_id
			  AND s.external_id = pending_artifact_links.session_external_id)`); err != nil {
		return 0, 0, fmt.Errorf("pruning resolved artifact links: %w", err)
	}

	var left int
	if err := tx.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM pending_relations)
		     + (SELECT COUNT(*) FROM pending_artifact_links)`).Scan(&left); err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return int(nRel + nArt), left, nil
}

func timeText(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func kindText(k canon.MessageKind) string {
	if k == "" {
		return string(canon.KindMessage)
	}
	return string(k)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
