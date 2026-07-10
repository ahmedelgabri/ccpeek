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
	Agent    string `json:"agent"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Size     int    `json:"size"`
	Sessions int    `json:"sessions"` // linked session count
}

// ArtifactsFilter narrows the artifacts op.
type ArtifactsFilter struct {
	Agent  string
	Kind   string
	Limit  int
	Offset int
}

// Artifacts lists artifacts, optionally by kind, newest natural order.
func (s *Service) Artifacts(ctx context.Context, f ArtifactsFilter) ([]ArtifactSummary, error) {
	if f.Limit <= 0 || f.Limit > 500 {
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

	rows, err := s.store.DB().QueryContext(ctx, fmt.Sprintf(`
		SELECT a.slug, ar.kind, ar.name, LENGTH(ar.content),
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
}

// Artifact fetches one artifact with content and linked sessions.
// Rendering (markdown → sanitized HTML) is applied by the caller-provided
// renderer when the kind is prose-shaped; the query layer stays
// presentation-free otherwise.
func (s *Service) Artifact(ctx context.Context, agentSlug, kind, name string, render func(kind, content string) string) (*ArtifactDetail, error) {
	d := &ArtifactDetail{}
	var id int64
	err := s.store.DB().QueryRowContext(ctx, `
		SELECT ar.id, a.slug, ar.kind, ar.name, LENGTH(ar.content),
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

	rows, err := s.store.DB().QueryContext(ctx, `
		SELECT se.external_id
		FROM artifact_sessions ass
		JOIN sessions se ON se.id = ass.session_id
		WHERE ass.artifact_id = ?
		ORDER BY se.modified_at DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		d.SessionIDs = append(d.SessionIDs, sid)
	}
	d.Sessions = len(d.SessionIDs)
	return d, rows.Err()
}
