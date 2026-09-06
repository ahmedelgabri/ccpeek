package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
)

// Writer applies canonical records inside a single transaction — the
// pipeline opens one Writer per changed source file (docs/v2-plan.md §5.6).
// It resolves natural keys ((agent, external_id) for sessions,
// (agent, kind, name) for artifacts) to row ids, caching within the
// transaction, and routes links whose target session isn't ingested yet to
// the pending tables for end-of-run resolution.
type Writer struct {
	tx            *sql.Tx
	ctx           context.Context
	agents        map[canon.AgentSlug]int64
	sessions      map[sessionKey]int64
	orphanUsage   []usageKey
	dirtySessions map[int64]bool
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
func (w *Writer) Commit() error {
	if err := w.restoreUsageOwners(); err != nil {
		return err
	}
	if _, err := w.tx.ExecContext(w.ctx, `INSERT OR REPLACE INTO meta(key,value) VALUES ('derived_dirty','1')`); err != nil {
		return err
	}
	return w.tx.Commit()
}

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
	return id, w.markSessionDirty(id)
}

// AdvanceSession updates an existing session from attributes folded out
// of newly-appended source bytes only: modification time, branch, and
// content hash move forward; creation-time attributes (created_at, and
// title/cwd once set) stay as the full parse recorded them — the tail
// never saw the lines they came from. An explicit native rename is the
// exception and replaces the stored fallback title. Returns ErrUnknownSession
// when the session was never fully parsed; callers fall back to a full parse.
func (w *Writer) AdvanceSession(sess canon.Session, contentHash string) (int64, error) {
	id, err := w.sessionID(sess.Agent, sess.ExternalID)
	if err != nil {
		return 0, err
	}
	if _, err := w.tx.ExecContext(w.ctx, `
		UPDATE sessions SET
			modified_at = COALESCE(?, modified_at),
			git_branch = CASE WHEN ? <> '' THEN ? ELSE git_branch END,
			title = CASE
				WHEN ? AND ? <> '' THEN ?
				WHEN title = '' THEN ?
				ELSE title
			END,
			cwd = CASE WHEN cwd = '' THEN ? ELSE cwd END,
			source_path = ?,
			content_hash = ?
		WHERE id = ?`,
		timeText(sess.ModifiedAt), sess.GitBranch, sess.GitBranch,
		sess.TitleOverride, sess.Title, sess.Title, sess.Title,
		sess.CWD, sess.SourcePath, contentHash, id); err != nil {
		return 0, fmt.Errorf("advancing session %s/%s: %w", sess.Agent, sess.ExternalID, err)
	}
	return id, w.markSessionDirty(id)
}

// ClearSessionChildren removes messages, tool calls, and search documents
// for a session prior to re-inserting them when its source changed.
func (w *Writer) ClearSessionChildren(sessionID int64) error {
	if err := w.rememberUsage(sessionID); err != nil {
		return err
	}
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
// (content_id, request_id), with an empty request id retained as the legacy
// key rather than disabling dedupe. The first-seen message owns the usage;
// a more complete duplicate updates that row's values without moving cost
// between sessions. The message row itself is always written so transcripts
// stay complete.
func (w *Writer) InsertMessage(sessionID int64, agent canon.AgentSlug, msg canon.Message) error {
	if err := w.markSessionDirty(sessionID); err != nil {
		return err
	}
	requestID := ""
	if msg.Usage != nil {
		requestID = msg.Usage.RequestID
		if msg.ContentID != "" {
			agentID, err := w.EnsureAgent(agent)
			if err != nil {
				return err
			}
			usage, err := bestUsage(w.ctx, w.tx, usageKey{agentID, msg.ContentID, requestID}, *msg.Usage)
			if err != nil {
				return err
			}
			msg.Usage = &usage
		}
	}
	res, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO messages
			(session_id, seq, external_id, parent_external_id, content_id,
			 role, kind, created_at, provider, model, cwd, is_sidechain, content, text_content, usage_request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, msg.Seq, msg.ExternalID, msg.ParentExternalID, msg.ContentID,
		string(msg.Role), kindText(msg.Kind), timeText(msg.CreatedAt),
		msg.Provider, msg.Model, msg.CWD, boolInt(msg.IsSidechain), string(msg.Content), msg.Text, requestID)
	if err != nil {
		return fmt.Errorf("inserting message seq %d: %w", msg.Seq, err)
	}
	if msg.Usage == nil {
		return nil
	}

	reportedNanos, err := exactReportedCost(msg.Usage.ReportedCostUSD)
	if err != nil {
		return fmt.Errorf("normalizing reported cost for message seq %d: %w", msg.Seq, err)
	}

	if msg.ContentID != "" {
		agentID, err := w.EnsureAgent(agent)
		if err != nil {
			return err
		}
		// The literal content_id <> '' predicate makes the partial index
		// eligible; without it this lookup turns first-run ingest quadratic.
		var existingID, existingOut, existingTotal int64
		var existingReported sql.NullFloat64
		var existingReportedNanos sql.NullInt64
		err = w.tx.QueryRowContext(w.ctx, `
			SELECT u.message_id, u.output_tokens,
			       u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens,
			       u.reported_cost_usd, u.reported_cost_nanos
			FROM message_usage u
			JOIN messages m ON m.id = u.message_id
			JOIN sessions s ON s.id = m.session_id
			WHERE s.agent_id = ? AND m.content_id = ? AND m.content_id <> ''
			  AND u.request_id = ?
			LIMIT 1`,
			agentID, msg.ContentID, msg.Usage.RequestID).
			Scan(&existingID, &existingOut, &existingTotal, &existingReported, &existingReportedNanos)
		switch {
		case err == nil:
			u := msg.Usage
			candidateTotal := u.InputTokens + u.OutputTokens +
				u.CacheReadTokens + u.CacheWriteTokens
			betterCost := u.ReportedCostUSD != nil && *u.ReportedCostUSD != 0 &&
				(!existingReported.Valid || existingReported.Float64 == 0)
			moreComplete := u.OutputTokens > existingOut ||
				(u.OutputTokens == existingOut && candidateTotal > existingTotal) ||
				(u.OutputTokens == existingOut && candidateTotal == existingTotal && betterCost)
			if moreComplete {
				if err := w.markMessageDirty(existingID); err != nil {
					return err
				}
				selectedCost := u.ReportedCostUSD
				selectedNanos := reportedNanos
				if existingReported.Valid &&
					(selectedCost == nil || (*selectedCost == 0 && existingReported.Float64 != 0)) {
					existing := existingReported.Float64
					selectedCost = &existing
					if existingReportedNanos.Valid {
						selectedNanos = existingReportedNanos.Int64
					} else {
						selectedNanos, err = exactReportedCost(selectedCost)
						if err != nil {
							return err
						}
					}
				}
				if _, err := w.tx.ExecContext(w.ctx, `
					UPDATE message_usage SET
						input_tokens = ?, output_tokens = ?, cache_read_tokens = ?,
						cache_write_tokens = ?, cache_write_1h_tokens = ?,
						reasoning_tokens = ?, service_tier = ?, reported_cost_usd = ?,
						reported_cost_nanos = ?
					WHERE message_id = ?`,
					u.InputTokens, u.OutputTokens, u.CacheReadTokens,
					u.CacheWriteTokens, u.CacheWrite1hTokens, u.ReasoningTokens,
					u.ServiceTier, selectedCost, selectedNanos, existingID); err != nil {
					return fmt.Errorf("updating deduplicated usage: %w", err)
				}
			}
			return nil
		case errors.Is(err, sql.ErrNoRows):
			// First sighting: fall through and attach usage to this message.
		default:
			return fmt.Errorf("usage dedupe lookup: %w", err)
		}
	}

	msgID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("message rowid: %w", err)
	}
	return w.insertUsage(msgID, *msg.Usage)
}

func (w *Writer) insertUsage(msgID int64, u canon.Usage) error {
	reportedNanos, err := exactReportedCost(u.ReportedCostUSD)
	if err != nil {
		return err
	}
	if _, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO message_usage
			(message_id, input_tokens, output_tokens, cache_read_tokens,
			 cache_write_tokens, cache_write_1h_tokens, reasoning_tokens,
			 service_tier, reported_cost_usd, reported_cost_nanos, request_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		msgID, u.InputTokens, u.OutputTokens, u.CacheReadTokens,
		u.CacheWriteTokens, u.CacheWrite1hTokens, u.ReasoningTokens,
		u.ServiceTier, u.ReportedCostUSD, reportedNanos, u.RequestID); err != nil {
		return fmt.Errorf("inserting usage for message seq %d: %w", msgID, err)
	}
	return nil
}

func exactReportedCost(cost *float64) (any, error) {
	if cost == nil {
		return nil, nil
	}
	amount, err := pricing.AmountFromUSD(*cost)
	if err != nil {
		return nil, err
	}
	return int64(amount), nil
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
	// The excerpt bound is applied here, where every adapter's calls
	// converge, rather than trusted from each of them.
	_, err := w.tx.ExecContext(w.ctx, `
		INSERT INTO tool_calls
			(session_id, message_seq, seq, external_id, name, kind, input_json,
			 result_status, result_excerpt, file_path, command, old_text, new_text,
			 started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID, tc.MessageSeq, tc.Seq, tc.ExternalID, tc.Name, string(kind), input,
		tc.ResultStatus, tc.ResultExcerpt, tc.FilePath,
		tc.Command,
		canon.TruncateBytes(tc.OldText, canon.ToolEditExcerptLimit),
		canon.TruncateBytes(tc.NewText, canon.ToolEditExcerptLimit),
		timeText(tc.StartedAt))
	if err != nil {
		return fmt.Errorf("inserting tool call seq %d (%s): %w", tc.Seq, tc.Name, err)
	}
	return nil
}

// UpdateToolCallResult attaches a late result to a stored tool call by
// its agent-native call id. A miss is not an error: the call may predate
// the external_id column or belong to source bytes that never parsed.
func (w *Writer) UpdateToolCallResult(sessionID int64, res canon.ToolResult) error {
	_, err := w.tx.ExecContext(w.ctx, `
		UPDATE tool_calls SET result_status = ?, result_excerpt = ?, result_content = ?
		WHERE session_id = ? AND external_id = ?`,
		res.Status, res.Excerpt, res.Content, sessionID, res.CallExternalID)
	if err != nil {
		return fmt.Errorf("updating tool call result %s: %w", res.CallExternalID, err)
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

// searchableArtifactKinds are the kinds worth full-text indexing. The
// excluded ones are machine-generated (the usage report is a whole HTML
// document, a shell snapshot is an environment dump) or hold their
// substance in metadata rather than content (file history) — indexing them
// cost a second copy of every byte in search_docs plus the FTS5 tokens,
// and returned hits nobody searches for.
var searchableArtifactKinds = map[canon.ArtifactKind]bool{
	canon.ArtifactPlan:       true,
	canon.ArtifactMemory:     true,
	canon.ArtifactTodoList:   true,
	canon.ArtifactTaskGroup:  true,
	canon.ArtifactPaste:      true,
	canon.ArtifactUsageFacet: true,
}

// WriteMessage stores one message and its search document together.
//
// Canonical text lives on messages, independently of the rebuildable search
// index. This method updates both representations in the source transaction.
func (w *Writer) WriteMessage(sessionID int64, agent canon.AgentSlug, msg canon.Message) error {
	if err := w.InsertMessage(sessionID, agent, msg); err != nil {
		return err
	}
	return w.InsertSearchDoc(sessionID, 0, "message", msg.Seq, "", msg.Text)
}

// WriteArtifact stores one artifact and brings its search index in line
// with it: content is bounded at canon.ArtifactContentLimit, stale docs are
// cleared, and only the kinds worth searching get a document. It reports
// whether the content was truncated so a caller with a diagnostics channel
// can say so.
//
// Every artifact in the store goes through here — live ingest and the v1
// import alike. Each used to spell the sequence out for itself, and the
// import's copy had already fallen behind: it indexed every kind including
// the whole-HTML usage report, and bounded nothing.
func (w *Writer) WriteArtifact(a canon.Artifact, contentHash string) (id int64, truncated bool, err error) {
	a.Content, truncated = canon.TruncateArtifactContent(a.Content)
	id, err = w.UpsertArtifact(a, contentHash)
	if err != nil {
		return 0, truncated, err
	}
	if err := w.ClearArtifactSearchDocs(id); err != nil {
		return 0, truncated, err
	}
	if searchableArtifactKinds[a.Kind] {
		if err := w.InsertSearchDoc(0, id, string(a.Kind), 0, a.Name, a.Content); err != nil {
			return 0, truncated, err
		}
	}
	return id, truncated, nil
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

// ClearHistorySource removes a history source's rows before a re-parse
// re-inserts them — history files are parsed whole whenever they change,
// and appends are the norm, so insert-only writes would duplicate every
// existing entry each time. Rows with an empty source_path from builds
// that predate this provenance are cleared for the same agent too (they
// can only have come from this source and would otherwise duplicate
// forever).
func (w *Writer) ClearHistorySource(agent canon.AgentSlug, sourcePath string) error {
	agentID, err := w.EnsureAgent(agent)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `
		DELETE FROM history
		WHERE source_path = ? OR (agent_id = ? AND source_path = '')`,
		sourcePath, agentID)
	if err != nil {
		return fmt.Errorf("clearing history source %s: %w", sourcePath, err)
	}
	return nil
}

// InsertHistory writes one prompt-history entry, resolving its session link
// when possible.
func (w *Writer) InsertHistory(h canon.HistoryEntry, sourcePath string) error {
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
		INSERT INTO history (agent_id, display, timestamp, session_id, source_path)
		VALUES (?, ?, ?, ?, ?)`,
		agentID, h.Display, h.Timestamp.UnixMilli(), sessionID, sourcePath)
	if err != nil {
		return fmt.Errorf("inserting history entry: %w", err)
	}
	return nil
}

// RecordSourceFile stores the content hash, stat signature, append cursor,
// and adapter parse version for incremental comparison.
func (w *Writer) RecordSourceFile(path string, agent canon.AgentSlug, contentHash, statSig, parseState string, parseVersion int) error {
	agentID, err := w.EnsureAgent(agent)
	if err != nil {
		return err
	}
	_, err = w.tx.ExecContext(w.ctx, `
		INSERT INTO source_files
			(path, agent_id, content_hash, stat_sig, parse_state, parse_version, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			agent_id = excluded.agent_id,
			content_hash = excluded.content_hash,
			stat_sig = excluded.stat_sig,
			parse_state = excluded.parse_state,
			parse_version = excluded.parse_version,
			indexed_at = excluded.indexed_at`,
		path, agentID, contentHash, statSig, parseState, parseVersion,
		time.Now().UTC().Format(time.RFC3339))
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

// pendingLinkAttemptLimit is how many DATA-CHANGING ingest passes a parked
// link may survive before it is dropped as unresolvable (see
// ResolvePending's ageAttempts — a pass that indexed nothing cannot count
// against it). Links waiting on a
// not-yet-ingested endpoint resolve on the pass that indexes it; a link
// still parked after this many passes is pointing at something that does
// not exist (a task directory that is not a session id, a relation to a
// transcript the user deleted), and keeping it forever both grows the
// table without bound and misreports unresolved_links as a live signal.
const pendingLinkAttemptLimit = 5

// ResolvePending links parked relations and artifact links whose endpoint
// sessions have since been ingested. Called once at the end of each ingest
// run. What remains is counted as unresolved_links in run telemetry.
//
// ageAttempts says whether this pass may count AGAINST the parked rows.
// Only a pass that actually ingested something can turn a parked link into
// a resolved one, so only such a pass is evidence that the endpoint is
// absent rather than late. Ageing on every call made the limit a function
// of wall-clock activity instead of ingest activity: `ccpeek --watch`
// fires a pass per debounce, most of which change nothing relevant, so
// five "attempts" could elapse in minutes and permanently drop a link
// whose endpoint arrives later (a Pi parentSession restored from a backup
// an hour on, a transcript copied in after its todo file).
func (s *Store) ResolvePending(ctx context.Context, ageAttempts bool) (resolved, remaining int, err error) {
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

	// Anything still parked has now failed a pass that COULD have resolved
	// it. Bump its counter and drop what has failed too many — the endpoint
	// is not late, it is absent. (Written out per table rather than looped:
	// the loop it replaced decided whether to bind an argument by testing
	// whether the statement text started with "DELETE".)
	if ageAttempts {
		for _, table := range []string{"pending_relations", "pending_artifact_links"} {
			if _, err := tx.ExecContext(ctx,
				`UPDATE `+table+` SET attempts = attempts + 1`); err != nil {
				return 0, 0, fmt.Errorf("ageing %s: %w", table, err)
			}
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM `+table+` WHERE attempts > ?`, pendingLinkAttemptLimit); err != nil {
				return 0, 0, fmt.Errorf("pruning %s: %w", table, err)
			}
		}
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

// PruneMissingSources removes derived rows whose recorded source no longer
// exists on disk (v1's --prune semantics). exists is injectable for tests.
// Imported-v1 rows are exempt: their sources were already gone at import
// time — that retention is the point.
//
// Removed owners' request keys are queued before deletion. Writer.Commit
// reassigns their durable usage claims only after all stale paths are gone,
// choosing a surviving copy with the same agent, content and request id.
func (s *Store) PruneMissingSources(ctx context.Context, exists func(path string) bool) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM source_files`)
	if err != nil {
		return 0, err
	}
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return 0, err
		}
		if !exists(p) {
			stale = append(stale, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(stale) == 0 {
		return 0, nil
	}

	w, err := s.BeginWrite(ctx)
	if err != nil {
		return 0, err
	}
	defer w.Rollback()
	tx := w.tx
	for _, p := range stale {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM sessions WHERE source_path = ? AND origin = 'ingest'`, p)
		if err != nil {
			return 0, err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return 0, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
		for _, id := range ids {
			if err := w.rememberUsage(id); err != nil {
				return 0, err
			}
		}
		for _, q := range []string{
			`DELETE FROM search_docs WHERE session_id IN
			   (SELECT id FROM sessions WHERE source_path = ? AND origin = 'ingest')`,
			`DELETE FROM sessions WHERE source_path = ? AND origin = 'ingest'`,
			`DELETE FROM search_docs WHERE artifact_id IN
			   (SELECT id FROM artifacts WHERE source_path = ?)`,
			`DELETE FROM artifacts WHERE source_path = ?`,
			// history carries source_path too, and its rows are only ever
			// cleared by re-parsing the file they came from — so a deleted
			// history.jsonl left every command in the index with no path
			// back out. --prune promises the opposite.
			`DELETE FROM history WHERE source_path = ?`,
			`DELETE FROM source_files WHERE path = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, p); err != nil {
				return 0, fmt.Errorf("pruning %s: %w", p, err)
			}
		}
	}
	if err := w.Commit(); err != nil {
		return 0, err
	}
	return len(stale), nil
}
