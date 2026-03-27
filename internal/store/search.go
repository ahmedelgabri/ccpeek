package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

const (
	searchGroupConversations = "conversations"
	searchGroupCommands      = "commands"
	searchGroupMemories      = "memories"
	searchGroupPlans         = "plans"
	searchGroupTodos         = "todos"
	searchGroupTasks         = "tasks"
	searchGroupPasteCache    = "paste_cache"
	searchGroupSnapshots     = "shell_snapshots"
	searchGroupUsageData     = "usage_data"
)

type searchGroupDef struct {
	Key   string
	Type  string
	Color string
}

var searchGroupDefs = []searchGroupDef{
	{Key: searchGroupConversations, Type: "Conversations", Color: "sky"},
	{Key: searchGroupCommands, Type: "Commands", Color: "lime"},
	{Key: searchGroupMemories, Type: "Memories", Color: "cyan"},
	{Key: searchGroupPlans, Type: "Plans", Color: "emerald"},
	{Key: searchGroupTodos, Type: "Todos", Color: "rose"},
	{Key: searchGroupTasks, Type: "Tasks", Color: "indigo"},
	{Key: searchGroupPasteCache, Type: "Paste Cache", Color: "orange"},
	{Key: searchGroupSnapshots, Type: "Shell Snapshots", Color: "amber"},
	{Key: searchGroupUsageData, Type: "Usage Data", Color: "fuchsia"},
}

// SearchHit is a generic search result from any data type.
type SearchHit struct {
	Title    string
	Snippet  string
	URL      string
	Subtitle string
}

// SearchGroup holds search results for a single data type.
type SearchGroup struct {
	Type  string // display name: "Conversations", "Memories", etc.
	Color string // tailwind color name for badge
	Hits  []SearchHit
}

// SearchAll performs a search across all indexed data types.
// Returns groups in a fixed display order; empty groups are omitted.
func (s *Store) SearchAll(ctx context.Context, query string, perTypeLimit int) ([]SearchGroup, error) {
	normalizedQuery := normalizeSearchQuery(query)
	if normalizedQuery == "" {
		return nil, nil
	}

	matchQuery := buildFTSQuery(normalizedQuery)
	if matchQuery == "" {
		return nil, nil
	}

	var groups []SearchGroup
	for _, def := range searchGroupDefs {
		hits, err := s.searchIndexedGroup(ctx, def.Key, matchQuery, perTypeLimit)
		if err != nil {
			return nil, err
		}
		if len(hits) > 0 {
			groups = append(groups, SearchGroup{Type: def.Type, Color: def.Color, Hits: hits})
		}
	}

	for i := range groups {
		for j := range groups[i].Hits {
			groups[i].Hits[j].URL = withTextFragment(groups[i].Hits[j].URL, normalizedQuery)
		}
	}

	return groups, nil
}

func normalizeSearchQuery(query string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(query)), " ")
}

func buildFTSQuery(query string) string {
	if query == "" {
		return ""
	}
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}

func (s *Store) searchIndexedGroup(ctx context.Context, groupType, matchQuery string, limit int) ([]SearchHit, error) {
	rows := []struct {
		Title    string `db:"title"`
		Subtitle string `db:"subtitle"`
		URL      string `db:"url"`
		Snippet  string `db:"snippet"`
	}{}

	err := s.db.SelectContext(ctx, &rows, `
		SELECT title, subtitle, url,
		       snippet(search_documents_fts, 4, '[[HL_START]]', '[[HL_END]]', '...', 40) AS snippet
		FROM search_documents_fts
		WHERE group_type = ? AND search_documents_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, groupType, matchQuery, limit)
	if err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		hits[i] = SearchHit{
			Title:    r.Title,
			Snippet:  r.Snippet,
			URL:      r.URL,
			Subtitle: r.Subtitle,
		}
	}
	return hits, nil
}

// anchor builds a URL-safe fragment from a prefix and value,
// matching the toAnchor template helper format.
func anchor(prefix, value string) string {
	safe := strings.NewReplacer(":", "-", ".", "-").Replace(value)
	return "#" + prefix + "-" + safe
}

// withTextFragment appends a Text Fragment directive (:~:text=) to a URL.
// If the URL already has a # fragment, the directive is appended after it.
// See https://developer.mozilla.org/en-US/docs/Web/URI/Reference/Fragment/Text_fragments
func withTextFragment(rawURL, query string) string {
	encoded := url.QueryEscape(query)
	if strings.Contains(rawURL, "#") {
		return rawURL + ":~:text=" + encoded
	}
	return rawURL + "#:~:text=" + encoded
}

// likeSnippet extracts a snippet from text centered around the first
// case-insensitive occurrence of query, with highlight markers.
func likeSnippet(text, query string, contextLen int) string {
	lower := strings.ToLower(text)
	q := strings.ToLower(query)
	pos := strings.Index(lower, q)
	if pos < 0 {
		return ""
	}

	start := max(pos-contextLen, 0)
	end := min(pos+len(query)+contextLen, len(text))
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

	lowerSnippet := strings.ToLower(snippet)
	matchPos := strings.Index(lowerSnippet, q)
	if matchPos >= 0 {
		snippet = snippet[:matchPos] + "[[HL_START]]" + snippet[matchPos:matchPos+len(query)] + "[[HL_END]]" + snippet[matchPos+len(query):]
	}

	return prefix + snippet + suffix
}

// truncateStr truncates a string, adding ellipsis if needed.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// formatBytesStore formats a file size for display in search results.
func formatBytesStore(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB"}
	i := 0
	v := float64(bytes)
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", bytes)
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
