package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ArtifactSummary is one row of the `artifacts` op.
type ArtifactSummary struct {
	Agent string `json:"agent"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	// Size is BYTES. SQLite's LENGTH() on a TEXT value counts characters,
	// so for any non-ASCII content the figure the UI formatted as KB/MB
	// was under the real one; the CAST makes it a byte count.
	Size     int `json:"size"`
	Sessions int `json:"sessions"` // linked session count
}

// ArtifactsFilter narrows the artifacts op.
type ArtifactsFilter struct {
	Agent  string
	Kind   string
	Limit  int
	Offset int
}

// Artifacts lists artifacts, optionally by kind, newest natural order.
// The default limit is a page size, not a ceiling: callers page onward
// with offset, and an explicit larger limit is honored — the old 500
// clamp silently truncated corpora past it.
func (s *Service) Artifacts(ctx context.Context, f ArtifactsFilter) ([]ArtifactSummary, error) {
	if f.Limit <= 0 {
		f.Limit = 100
	}
	var where []string
	var args []any
	if f.Agent != "" {
		where = append(where, `a.slug = ?`)
		args = append(args, f.Agent)
	}
	if f.Kind != "" {
		where = append(where, `ar.kind = ?`)
		args = append(args, f.Kind)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit, f.Offset)

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT a.slug, ar.kind, ar.name, LENGTH(CAST(ar.content AS BLOB)),
		       (SELECT COUNT(*) FROM artifact_sessions ass WHERE ass.artifact_id = ar.id)
		FROM artifacts ar
		JOIN agents a ON a.id = ar.agent_id
		%s
		ORDER BY ar.kind, ar.name
		LIMIT ? OFFSET ?`, clause), args...)
	if err != nil {
		return nil, fmt.Errorf("listing artifacts: %w", err)
	}
	defer rows.Close()
	var out []ArtifactSummary
	for rows.Next() {
		var a ArtifactSummary
		if err := rows.Scan(&a.Agent, &a.Kind, &a.Name, &a.Size, &a.Sessions); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ArtifactDetail is the `artifact` op result.
type ArtifactDetail struct {
	ArtifactSummary
	Content     string   `json:"content,omitempty"`
	ContentHTML string   `json:"contentHTML,omitempty"` // server-rendered (markdown kinds)
	Metadata    string   `json:"metadata,omitempty"`    // raw JSON payload
	SessionIDs  []string `json:"sessionIds,omitempty"`  // linked session external ids
	// SessionAnchors maps a linked session's external id to the transcript
	// seq of the tool call that produced this artifact (a TodoWrite for a
	// todo list, an ExitPlanMode for a plan), when one is found — letting
	// the UI deep-link the artifact to the message it came from. Kinds with
	// no producing tool call (memories, snapshots, file history) have none.
	SessionAnchors map[string]int `json:"sessionAnchors,omitempty"`
}

// producerTool names the tool call whose latest occurrence in a session
// produced an artifact of a given kind. Only kinds emitted by a tool call
// appear here; the rest have no message-level provenance. Plans are not
// listed: a session can hold several plans, so their anchor is resolved
// by matching plan text against ExitPlanMode inputs instead of "last
// call wins" (see the plan block in Artifact).
var producerTool = map[string]string{
	"todo_list": "TodoWrite",
}

// Artifact fetches one artifact with content and linked sessions.
// Rendering (markdown → sanitized HTML) is applied by the caller-provided
// renderer when the kind is prose-shaped; the query layer stays
// presentation-free otherwise.
func (s *Service) Artifact(ctx context.Context, agentSlug, kind, name string, render func(kind, content string) string) (*ArtifactDetail, error) {
	d := &ArtifactDetail{}
	var id int64
	err := s.store.ReadDB().QueryRowContext(ctx, `
		SELECT ar.id, a.slug, ar.kind, ar.name, LENGTH(CAST(ar.content AS BLOB)),
		       ar.content, ar.metadata_json
		FROM artifacts ar
		JOIN agents a ON a.id = ar.agent_id
		WHERE a.slug = ? AND ar.kind = ? AND ar.name = ?`,
		agentSlug, kind, name).
		Scan(&id, &d.Agent, &d.Kind, &d.Name, &d.Size, &d.Content, &d.Metadata)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: artifact %s/%s/%s", ErrNotFound, agentSlug, kind, name)
	}
	if err != nil {
		return nil, err
	}
	if d.Metadata == "{}" {
		d.Metadata = ""
	}
	if render != nil {
		d.ContentHTML = render(d.Kind, d.Content)
	}

	// anchor_seq is what the plan/memory resolvers recorded when they
	// matched the link — they knew exactly which call produced it. Links
	// from other evidence carry no anchor, so those fall back to the
	// generic heuristic: the last producing tool call in the session (the
	// correlated subquery yields NULL when the kind has no producer or the
	// session has no such call).
	//
	// The read path used to have neither: it re-ran the resolvers' matching
	// on every request — re-normalizing plan markdown, re-deriving memory
	// path suffixes — with two extra queries per artifact, and that is the
	// only reason those helpers were exported from db at all.
	rows, err := s.store.ReadDB().QueryContext(ctx, `
		SELECT se.external_id,
		       COALESCE(ass.anchor_seq,
		                (SELECT tc.message_seq FROM tool_calls tc
		                 WHERE tc.session_id = se.id AND tc.name = ?
		                 ORDER BY tc.seq DESC LIMIT 1)) AS anchor
		FROM artifact_sessions ass
		JOIN sessions se ON se.id = ass.session_id
		WHERE ass.artifact_id = ?
		ORDER BY se.modified_at DESC`, producerTool[kind], id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		var anchor sql.NullInt64
		if err := rows.Scan(&sid, &anchor); err != nil {
			return nil, err
		}
		d.SessionIDs = append(d.SessionIDs, sid)
		if anchor.Valid {
			if d.SessionAnchors == nil {
				d.SessionAnchors = map[string]int{}
			}
			d.SessionAnchors[sid] = int(anchor.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	d.Sessions = len(d.SessionIDs)

	return d, nil
}
