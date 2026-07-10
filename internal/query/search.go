package query

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SearchHit is one result of the `search` op. Every hit resolves to a
// session (directly for messages, via links for artifacts).
type SearchHit struct {
	DocType   string `json:"docType"` // message | plan | todo_list | …
	Agent     string `json:"agent"`
	SessionID string `json:"sessionId,omitempty"` // external id when resolvable
	Seq       int    `json:"seq,omitempty"`       // message anchor within the session
	Artifact  string `json:"artifact,omitempty"`  // artifact name for sidecar hits
	Title     string `json:"title,omitempty"`
	Snippet   string `json:"snippet"`
}

// SearchFilter narrows the search op.
type SearchFilter struct {
	Agent string
	Limit int
}

// Search runs full-text search across everything indexed.
func (s *Service) Search(ctx context.Context, q string, f SearchFilter) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > 100 {
		f.Limit = 100
	}

	where := ""
	args := []any{ftsQuery(q)}
	if f.Agent != "" {
		where = `AND COALESCE(sa.slug, aa.slug) = ?`
		args = append(args, f.Agent)
	}
	args = append(args, f.Limit)

	rows, err := s.store.DB().QueryContext(ctx, fmt.Sprintf(`
		SELECT d.doc_type, COALESCE(sa.slug, aa.slug, ''),
		       COALESCE(se.external_id, ''), d.seq,
		       COALESCE(ar.name, ''), d.title,
		       snippet(search_fts, 0, '[', ']', '…', 12)
		FROM search_fts
		JOIN search_docs d ON d.id = search_fts.rowid
		LEFT JOIN sessions se ON se.id = d.session_id
		LEFT JOIN agents sa ON sa.id = se.agent_id
		LEFT JOIN artifacts ar ON ar.id = d.artifact_id
		LEFT JOIN agents aa ON aa.id = ar.agent_id
		WHERE search_fts MATCH ? %s
		ORDER BY rank
		LIMIT ?`, where), args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var sessionID, artifact sql.NullString
		if err := rows.Scan(&h.DocType, &h.Agent, &sessionID, &h.Seq,
			&artifact, &h.Title, &h.Snippet); err != nil {
			return nil, err
		}
		h.SessionID = sessionID.String
		h.Artifact = artifact.String
		out = append(out, h)
	}
	return out, rows.Err()
}

// ftsQuery quotes user input as FTS5 phrases per term, preventing syntax
// errors from raw MATCH operators.
func ftsQuery(q string) string {
	terms := strings.Fields(q)
	for i, t := range terms {
		terms[i] = `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
	}
	return strings.Join(terms, " ")
}
