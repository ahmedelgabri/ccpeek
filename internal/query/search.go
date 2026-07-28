package query

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SearchHit is one result of the `search` op. Message hits resolve to
// their session (SessionID + Seq anchor); artifact hits carry their own
// locator instead — Agent + DocType (the artifact kind) + Artifact (the
// name) address the artifact page directly, with no pretense of one
// canonical session.
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

// Snippet match delimiters. FTS5 wraps matched terms in these, and the
// caller splits on them to mark the hit.
//
// They are control characters, not brackets. With '[' and ']' the
// delimiters were indistinguishable from brackets in the CONTENT, and the
// corpus is source code and agent transcripts: a hit inside a markdown
// link, a slice expression, a JSON array or a log prefix produced stray
// and unbalanced marks. U+0002/U+0003 cannot occur in indexed text.
const (
	SnippetOpen  = "\x02"
	SnippetClose = "\x03"
)

// Search runs full-text search across everything indexed.
func (s *Service) Search(ctx context.Context, q string, f SearchFilter) ([]SearchHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if err := SearchLimit.apply(&f.Limit); err != nil {
		return nil, err
	}

	where := ""
	args := []any{ftsQuery(q)}
	if f.Agent != "" {
		where = `AND COALESCE(sa.slug, aa.slug) = ?`
		args = append(args, f.Agent)
	}
	args = append(args, f.Limit)

	rows, err := s.store.ReadDB().QueryContext(ctx, fmt.Sprintf(`
		SELECT d.doc_type, COALESCE(sa.slug, aa.slug, ''),
		       COALESCE(se.external_id, ''), d.seq,
		       COALESCE(ar.name, ''), d.title,
		       snippet(search_fts, 0, char(2), char(3), '…', 12)
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
