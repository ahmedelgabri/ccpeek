package server

import (
	"net/http"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

const searchPerTypeLimit = 20

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var groups []store.SearchGroup
	totalResults := 0
	if query != "" {
		var err error
		groups, err = h.store.SearchAll(ctx, query, searchPerTypeLimit)
		if err != nil {
			groups = nil
		}
		for _, g := range groups {
			totalResults += len(g.Hits)
		}
	}

	renderTemplate(w, h.tmpl, "search.html", map[string]any{
		"Title":        "Search",
		"CurrentPath":  "/search/",
		"Query":        query,
		"Groups":       groups,
		"TotalResults": totalResults,
	})
}

// extractSnippet returns a substring of text centered around the match at pos.
func extractSnippet(text string, pos, matchLen, contextLen int) string {
	start := max(pos-contextLen, 0)
	end := min(pos+matchLen+contextLen, len(text))
	snippet := text[start:end]
	snippet = strings.Join(strings.Fields(snippet), " ")
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(text) {
		suffix = "..."
	}
	return prefix + snippet + suffix
}
