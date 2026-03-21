package store

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

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
	if query == "" {
		return nil, nil
	}

	var groups []SearchGroup

	// 1. Conversations (FTS)
	if hits, err := s.searchConversations(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Conversations", Color: "sky", Hits: hits})
	}

	// 2. Commands (LIKE)
	if hits, err := s.searchCommands(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Commands", Color: "lime", Hits: hits})
	}

	// 3. Memories (LIKE)
	if hits, err := s.searchMemories(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Memories", Color: "cyan", Hits: hits})
	}

	// 4. Plans (LIKE)
	if hits, err := s.searchPlans(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Plans", Color: "emerald", Hits: hits})
	}

	// 5. Todos (LIKE)
	if hits, err := s.searchTodos(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Todos", Color: "rose", Hits: hits})
	}

	// 6. Tasks (LIKE)
	if hits, err := s.searchTasks(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Tasks", Color: "indigo", Hits: hits})
	}

	// 7. Paste Cache (LIKE)
	if hits, err := s.searchPasteCache(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Paste Cache", Color: "orange", Hits: hits})
	}

	// 8. Shell Snapshots (LIKE)
	if hits, err := s.searchSnapshots(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Shell Snapshots", Color: "amber", Hits: hits})
	}

	// 9. Usage Data (LIKE)
	if hits, err := s.searchUsageData(ctx, query, perTypeLimit); err == nil && len(hits) > 0 {
		groups = append(groups, SearchGroup{Type: "Usage Data", Color: "fuchsia", Hits: hits})
	}

	// Append Text Fragment directives to all URLs so browsers highlight
	// the matched text on the target page.
	for i := range groups {
		for j := range groups[i].Hits {
			groups[i].Hits[j].URL = withTextFragment(groups[i].Hits[j].URL, query)
		}
	}

	return groups, nil
}

func (s *Store) searchConversations(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	q := `
		SELECT m.role, m.timestamp,
			   s.session_id AS session_id_text, s.first_prompt,
			   p.dir_name, p.display_name,
			   snippet(messages_fts, 0, '[[HL_START]]', '[[HL_END]]', '...', 40) AS snippet
		FROM messages_fts
		JOIN messages m ON messages_fts.rowid = m.id
		JOIN sessions s ON m.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE messages_fts MATCH ?
		ORDER BY rank LIMIT ?`

	var rows []struct {
		Role           string `db:"role"`
		Timestamp      string `db:"timestamp"`
		SessionID      string `db:"session_id_text"`
		FirstPrompt    string `db:"first_prompt"`
		ProjectDirName string `db:"dir_name"`
		ProjectDisplay string `db:"display_name"`
		Snippet        string `db:"snippet"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, query, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		prompt := r.FirstPrompt
		if prompt == "" {
			prompt = r.SessionID
		}
		url := "/projects/" + r.ProjectDirName + "/" + r.SessionID + "/"
		if r.Timestamp != "" {
			url += anchor("msg", r.Timestamp)
		}
		hits[i] = SearchHit{
			Title:    prompt,
			Snippet:  r.Snippet,
			URL:      url,
			Subtitle: r.ProjectDisplay + " · " + r.Role,
		}
	}
	return hits, nil
}

func (s *Store) searchCommands(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT COALESCE(json_extract(tc.input_json, '$.command'), '') AS command,
			   tc.timestamp, s.session_id, s.first_prompt,
			   p.dir_name, p.display_name
		FROM tool_calls tc
		JOIN sessions s ON tc.session_id = s.id
		JOIN projects p ON s.project_id = p.id
		WHERE tc.tool_kind = 'shell' AND json_extract(tc.input_json, '$.command') LIKE ? ESCAPE '\'
		ORDER BY tc.timestamp DESC, tc.seq DESC LIMIT ?`

	var rows []struct {
		Command        string `db:"command"`
		Timestamp      string `db:"timestamp"`
		SessionID      string `db:"session_id"`
		FirstPrompt    string `db:"first_prompt"`
		ProjectDirName string `db:"dir_name"`
		ProjectDisplay string `db:"display_name"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		url := "/projects/" + r.ProjectDirName + "/" + r.SessionID + "/commands/"
		if r.Timestamp != "" {
			url += anchor("cmd", r.Timestamp)
		}
		hits[i] = SearchHit{
			Title:    truncateStr(r.Command, 120),
			Snippet:  likeSnippet(r.Command, query, 80),
			URL:      url,
			Subtitle: r.ProjectDisplay,
		}
	}
	return hits, nil
}

func (s *Store) searchMemories(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT m.project_dir, m.content,
			   COALESCE(p.display_name, m.project_dir) AS display_name
		FROM memories m
		LEFT JOIN projects p ON m.project_id = p.id
		WHERE m.content LIKE ? ESCAPE '\'
		LIMIT ?`

	var rows []struct {
		ProjectDir  string `db:"project_dir"`
		Content     string `db:"content"`
		DisplayName string `db:"display_name"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		hits[i] = SearchHit{
			Title:    r.DisplayName,
			Snippet:  likeSnippet(r.Content, query, 120),
			URL:      "/memories/" + r.ProjectDir + "/",
			Subtitle: "MEMORY.md",
		}
	}
	return hits, nil
}

func (s *Store) searchPlans(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT file_name, title, content
		FROM plans
		WHERE title LIKE ? ESCAPE '\' OR content LIKE ? ESCAPE '\'
		LIMIT ?`

	var rows []struct {
		FileName string `db:"file_name"`
		Title    string `db:"title"`
		Content  string `db:"content"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		snippet := likeSnippet(r.Content, query, 120)
		if snippet == "" {
			snippet = likeSnippet(r.Title, query, 120)
		}
		hits[i] = SearchHit{
			Title:    r.Title,
			Snippet:  snippet,
			URL:      "/plans/" + strings.TrimSuffix(r.FileName, ".md") + "/",
			Subtitle: r.FileName,
		}
	}
	return hits, nil
}

func (s *Store) searchTodos(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT ti.content, ti.status, ti.seq, t.file_name,
			   COALESCE(p.display_name, '') AS display_name
		FROM todo_items ti
		JOIN todos t ON ti.todo_id = t.id
		LEFT JOIN sessions s ON t.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		WHERE ti.content LIKE ? ESCAPE '\'
		LIMIT ?`

	var rows []struct {
		Content     string `db:"content"`
		Status      string `db:"status"`
		Seq         int    `db:"seq"`
		FileName    string `db:"file_name"`
		DisplayName string `db:"display_name"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		subtitle := r.Status
		if r.DisplayName != "" {
			subtitle = r.DisplayName + " · " + r.Status
		}
		hits[i] = SearchHit{
			Title:    truncateStr(r.Content, 100),
			Snippet:  likeSnippet(r.Content, query, 120),
			URL:      "/todos/" + strings.TrimSuffix(r.FileName, ".json") + "/" + fmt.Sprintf("#item-%d", r.Seq),
			Subtitle: subtitle,
		}
	}
	return hits, nil
}

func (s *Store) searchTasks(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT ti.item_id, ti.subject, ti.description, ti.status, tg.dir_name,
			   COALESCE(p.display_name, '') AS display_name
		FROM task_items ti
		JOIN task_groups tg ON ti.task_group_id = tg.id
		LEFT JOIN sessions s ON tg.session_id = s.id
		LEFT JOIN projects p ON s.project_id = p.id
		WHERE ti.subject LIKE ? ESCAPE '\' OR ti.description LIKE ? ESCAPE '\'
		LIMIT ?`

	var rows []struct {
		ItemID      string `db:"item_id"`
		Subject     string `db:"subject"`
		Description string `db:"description"`
		Status      string `db:"status"`
		DirName     string `db:"dir_name"`
		DisplayName string `db:"display_name"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		snippet := likeSnippet(r.Subject, query, 120)
		if snippet == "" {
			snippet = likeSnippet(r.Description, query, 120)
		}
		subtitle := r.Status
		if r.DisplayName != "" {
			subtitle = r.DisplayName + " · " + r.Status
		}
		url := "/tasks/" + r.DirName + "/"
		if r.ItemID != "" {
			url += "#task-" + r.ItemID
		}
		hits[i] = SearchHit{
			Title:    r.Subject,
			Snippet:  snippet,
			URL:      url,
			Subtitle: subtitle,
		}
	}
	return hits, nil
}

func (s *Store) searchPasteCache(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT file_name, content, size_bytes
		FROM paste_cache
		WHERE content LIKE ? ESCAPE '\'
		LIMIT ?`

	var rows []struct {
		FileName  string `db:"file_name"`
		Content   string `db:"content"`
		SizeBytes int64  `db:"size_bytes"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		hits[i] = SearchHit{
			Title:    r.FileName,
			Snippet:  likeSnippet(r.Content, query, 120),
			URL:      "/paste-cache/" + strings.TrimSuffix(r.FileName, ".txt") + "/",
			Subtitle: formatBytesStore(r.SizeBytes),
		}
	}
	return hits, nil
}

func (s *Store) searchSnapshots(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT file_name, content
		FROM shell_snapshots
		WHERE content LIKE ? ESCAPE '\'
		LIMIT ?`

	var rows []struct {
		FileName string `db:"file_name"`
		Content  string `db:"content"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		hits[i] = SearchHit{
			Title:    r.FileName,
			Snippet:  likeSnippet(r.Content, query, 120),
			URL:      "/shell-snapshots/" + strings.TrimSuffix(r.FileName, ".sh") + "/",
			Subtitle: "Shell snapshot",
		}
	}
	return hits, nil
}

func (s *Store) searchUsageData(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	like := "%" + escapeLike(query) + "%"
	q := `
		SELECT session_id_text, brief_summary, underlying_goal, outcome
		FROM usage_facets
		WHERE brief_summary LIKE ? ESCAPE '\' OR underlying_goal LIKE ? ESCAPE '\'
		LIMIT ?`

	var rows []struct {
		SessionID      string `db:"session_id_text"`
		BriefSummary   string `db:"brief_summary"`
		UnderlyingGoal string `db:"underlying_goal"`
		Outcome        string `db:"outcome"`
	}
	if err := s.db.SelectContext(ctx, &rows, q, like, like, limit); err != nil {
		return nil, err
	}

	hits := make([]SearchHit, len(rows))
	for i, r := range rows {
		title := r.BriefSummary
		if title == "" {
			title = r.UnderlyingGoal
		}
		snippet := likeSnippet(r.BriefSummary, query, 120)
		if snippet == "" {
			snippet = likeSnippet(r.UnderlyingGoal, query, 120)
		}
		hits[i] = SearchHit{
			Title:    truncateStr(title, 80),
			Snippet:  snippet,
			URL:      "/usage-data/" + r.SessionID + "/",
			Subtitle: r.Outcome,
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

	// Insert highlight markers around the match within the snippet
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
