package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

const pageSize = 50

type heatmapDay struct {
	Date  string
	Count int
	Level int // 0-4 intensity
}

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	stats, _ := h.store.GetStats()

	history, _ := h.store.ListHistory(50)
	dayCounts, _ := h.store.HistoryDayCounts()

	heatmap := buildHeatmapFromCounts(dayCounts)

	renderTemplate(w, h.tmpl, "dashboard.html", map[string]any{
		"Title":       "Dashboard",
		"CurrentPath": "/",
		"Stats":       stats,
		"Index": map[string]any{
			"Plans":          make([]any, stats.PlanCount),
			"ShellSnapshots": make([]any, stats.SnapshotCount),
			"Todos":          make([]any, stats.TodoCount),
			"Projects":       make([]any, stats.ProjectCount),
			"FileHistory":    make([]any, stats.FileHistCount),
		},
		"TotalSessions": stats.SessionCount,
		"RecentHistory": history,
		"Heatmap":       heatmap,
	})
}

// buildHeatmapFromCounts builds heatmap data from pre-aggregated day counts.
func buildHeatmapFromCounts(dayCounts map[string]int) []heatmapDay {
	now := time.Now()
	days := make([]heatmapDay, 364)
	maxCount := 0
	for i := range days {
		t := now.AddDate(0, 0, -(363 - i))
		day := t.Format("2006-01-02")
		count := dayCounts[day]
		if count > maxCount {
			maxCount = count
		}
		days[i] = heatmapDay{
			Date:  day,
			Count: count,
		}
	}

	for i := range days {
		if days[i].Count == 0 {
			days[i].Level = 0
		} else if maxCount <= 4 {
			days[i].Level = days[i].Count
		} else {
			days[i].Level = min(4, 1+(days[i].Count*3)/maxCount)
		}
	}

	return days
}

// buildHeatmap builds heatmap data from history entries (used in tests).
func buildHeatmap(history []model.HistoryEntry) []heatmapDay {
	dayCounts := make(map[string]int)
	for _, entry := range history {
		t := time.UnixMilli(entry.Timestamp)
		day := t.Format("2006-01-02")
		dayCounts[day]++
	}
	return buildHeatmapFromCounts(dayCounts)
}

func (h *handlers) plansList(w http.ResponseWriter, r *http.Request) {
	plans, err := h.store.ListPlans()
	if err != nil {
		http.Error(w, "loading plans: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderTemplate(w, h.tmpl, "plans_list.html", map[string]any{
		"Title":       "Plans",
		"CurrentPath": "/plans/",
		"Plans":       plans,
	})
}

func (h *handlers) planDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")

	entry, content, err := h.store.GetPlan(fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "plan_detail.html", map[string]any{
		"Title":       entry.Title,
		"CurrentPath": "/plans/",
		"Plan":        entry,
		"Content":     renderMarkdown(content),
	})
}

func (h *handlers) snapshotsList(w http.ResponseWriter, r *http.Request) {
	snapshots, err := h.store.ListShellSnapshots()
	if err != nil {
		http.Error(w, "loading snapshots: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderTemplate(w, h.tmpl, "snapshots_list.html", map[string]any{
		"Title":       "Shell Snapshots",
		"CurrentPath": "/shell-snapshots/",
		"Snapshots":   snapshots,
	})
}

func (h *handlers) snapshotDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")

	entry, content, err := h.store.GetShellSnapshot(fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "snapshot_detail.html", map[string]any{
		"Title":       entry.FileName,
		"CurrentPath": "/shell-snapshots/",
		"Snapshot":    entry,
		"Content":     wrapCode(content, "bash"),
	})
}

func (h *handlers) todosList(w http.ResponseWriter, r *http.Request) {
	todos, err := h.store.ListTodos()
	if err != nil {
		http.Error(w, "loading todos: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderTemplate(w, h.tmpl, "todos_list.html", map[string]any{
		"Title":       "Todos",
		"CurrentPath": "/todos/",
		"Todos":       todos,
	})
}

func (h *handlers) todoDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")

	entry, items, err := h.store.GetTodo(fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "todo_detail.html", map[string]any{
		"Title":       "Todo List",
		"CurrentPath": "/todos/",
		"Todo":        entry,
		"Items":       items,
	})
}

func (h *handlers) projectsList(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects()
	if err != nil {
		http.Error(w, "loading projects: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderTemplate(w, h.tmpl, "projects_list.html", map[string]any{
		"Title":       "Projects",
		"CurrentPath": "/projects/",
		"Projects":    projects,
	})
}

func (h *handlers) sessionsList(w http.ResponseWriter, r *http.Request) {
	dirName := r.PathValue("dirName")

	project, err := h.store.GetProject(dirName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "sessions_list.html", map[string]any{
		"Title":       project.DisplayName,
		"CurrentPath": "/projects/",
		"Project":     project,
	})
}

// lookupSession finds a project and session from the URL path values.
func (h *handlers) lookupSession(w http.ResponseWriter, r *http.Request) (*model.ProjectEntry, *model.SessionEntry, bool) {
	dirName := r.PathValue("dirName")
	sessionID := r.PathValue("sessionId")

	project, session, err := h.store.GetSession(dirName, sessionID)
	if err != nil || session == nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return project, session, true
}

func (h *handlers) conversation(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	offset := (page - 1) * pageSize
	messages, totalMsgs, err := h.store.GetSessionMessages(session.SessionID, offset, pageSize)
	if err != nil {
		http.Error(w, "loading messages: "+err.Error(), http.StatusInternalServerError)
		return
	}

	totalPages := (totalMsgs + pageSize - 1) / pageSize
	if page > totalPages {
		page = totalPages
	}
	if totalPages == 0 {
		totalPages = 1
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	renderTemplate(w, h.tmpl, "conversation.html", map[string]any{
		"Title":         title,
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "conversation",
		"HasCodeBlocks": hasCodeBlocks(session),
		"Messages":      messages,
		"TotalMsgs":     totalMsgs,
		"Page":          page,
		"TotalPages":    totalPages,
		"HasPrev":       page > 1,
		"HasNext":       page < totalPages,
		"PrevPage":      page - 1,
		"NextPage":      page + 1,
	})
}

func (h *handlers) conversationTodos(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if session.TodoFileName == "" {
		http.NotFound(w, r)
		return
	}

	_, items, err := h.store.GetTodo(strings.TrimSuffix(session.TodoFileName, ".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	renderTemplate(w, h.tmpl, "conversation_todos.html", map[string]any{
		"Title":         title + " - Todos",
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "todos",
		"HasCodeBlocks": hasCodeBlocks(session),
		"Items":         items,
	})
}

func (h *handlers) conversationFileHistory(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if !session.HasFileHistory {
		http.NotFound(w, r)
		return
	}

	_, detail, err := h.store.GetFileHistory(session.SessionID)
	if err != nil || detail == nil {
		http.NotFound(w, r)
		return
	}

	type VersionEntry struct {
		model.FileVersionInfo
		DiffHTML template.HTML
	}
	type HashGroup struct {
		Hash     string
		Versions []VersionEntry
	}
	var groups []HashGroup
	groupMap := make(map[string]int)

	for _, f := range detail.Files {
		ve := VersionEntry{FileVersionInfo: f}
		if idx, ok := groupMap[f.Hash]; ok {
			prev := groups[idx].Versions[len(groups[idx].Versions)-1]
			ve.DiffHTML = renderDiff(prev.Content, f.Content)
			groups[idx].Versions = append(groups[idx].Versions, ve)
		} else {
			groupMap[f.Hash] = len(groups)
			groups = append(groups, HashGroup{
				Hash:     f.Hash,
				Versions: []VersionEntry{ve},
			})
		}
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	renderTemplate(w, h.tmpl, "conversation_filehistory.html", map[string]any{
		"Title":         title + " - File History",
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "file-history",
		"HasCodeBlocks": hasCodeBlocks(session),
		"Groups":        groups,
		"TotalFiles":    len(detail.Files),
	})
}

type bashCommand struct {
	Command   string
	Timestamp string
}

func (h *handlers) conversationCommands(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}
	_ = project

	if session.BashCommandCount == 0 {
		http.NotFound(w, r)
		return
	}

	messages, err := h.store.GetAllSessionMessages(session.SessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var commands []bashCommand
	for _, m := range messages {
		if m.Message.Role != "assistant" {
			continue
		}
		for _, b := range m.Message.ContentBlocks() {
			if b.Type != "tool_use" || b.Name != "Bash" {
				continue
			}
			var input struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(b.Input, &input) == nil && input.Command != "" {
				commands = append(commands, bashCommand{
					Command:   input.Command,
					Timestamp: m.Timestamp,
				})
			}
		}
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	renderTemplate(w, h.tmpl, "conversation_commands.html", map[string]any{
		"Title":         title + " - Commands",
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "commands",
		"HasCodeBlocks": hasCodeBlocks(session),
		"Commands":      commands,
	})
}

type toolCall struct {
	Name      string
	Detail    string
	Timestamp string
}

type toolStat struct {
	Name  string
	Count int
}

func (h *handlers) conversationTools(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}
	_ = project

	if len(session.ToolUseCounts) == 0 {
		http.NotFound(w, r)
		return
	}

	messages, err := h.store.GetAllSessionMessages(session.SessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var calls []toolCall
	for _, m := range messages {
		if m.Message.Role != "assistant" {
			continue
		}
		for _, b := range m.Message.ContentBlocks() {
			if b.Type != "tool_use" || b.Name == "" {
				continue
			}
			detail := extractToolDetail(b)
			calls = append(calls, toolCall{
				Name:      b.Name,
				Detail:    detail,
				Timestamp: m.Timestamp,
			})
		}
	}

	var stats []toolStat
	for name, count := range session.ToolUseCounts {
		stats = append(stats, toolStat{Name: name, Count: count})
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Count > stats[j].Count
	})

	totalCalls := 0
	for _, s := range stats {
		totalCalls += s.Count
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	renderTemplate(w, h.tmpl, "conversation_tools.html", map[string]any{
		"Title":         title + " - Tools",
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "tools",
		"HasCodeBlocks": hasCodeBlocks(session),
		"Stats":         stats,
		"Calls":         calls,
		"TotalCalls":    totalCalls,
	})
}

func (h *handlers) conversationExport(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	messages, err := h.store.GetAllSessionMessages(session.SessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var buf strings.Builder
	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}
	buf.WriteString("# " + title + "\n\n")
	if session.Created != "" {
		buf.WriteString("**Date:** " + session.Created + "\n")
	}
	if session.GitBranch != "" {
		buf.WriteString("**Branch:** " + session.GitBranch + "\n")
	}
	buf.WriteString("**Project:** " + project.DisplayName + "\n\n---\n\n")

	for _, m := range messages {
		role := m.Message.Role
		if role == "" {
			role = m.Type
		}
		buf.WriteString("## " + strings.ToUpper(role[:1]) + role[1:] + "\n\n")

		blocks := m.Message.ContentBlocks()
		if blocks == nil {
			text := m.Message.ContentText()
			if text != "" {
				buf.WriteString(text + "\n\n")
			}
			continue
		}

		for _, b := range blocks {
			switch b.Type {
			case "text":
				buf.WriteString(b.Text + "\n\n")
			case "tool_use":
				buf.WriteString("**Tool:** " + b.Name + "\n\n")
				if b.Input != nil {
					var input map[string]any
					if json.Unmarshal(b.Input, &input) == nil {
						if cmd, ok := input["command"].(string); ok {
							buf.WriteString("```bash\n" + cmd + "\n```\n\n")
						} else if fp, ok := input["file_path"].(string); ok {
							buf.WriteString("`" + fp + "`\n\n")
						}
					}
				}
			case "tool_result":
				text := b.ToolResultText()
				if text != "" {
					if len(text) > 500 {
						text = text[:500] + "..."
					}
					buf.WriteString("```\n" + text + "\n```\n\n")
				}
			}
		}
	}

	filename := session.SessionID + ".md"
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Write([]byte(buf.String()))
}

type codeBlock struct {
	Tool      string
	FilePath  string
	Content   string
	OldString string
	Timestamp string
}

func (h *handlers) conversationCode(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}
	_ = project

	if !hasCodeBlocks(session) {
		http.NotFound(w, r)
		return
	}

	messages, err := h.store.GetAllSessionMessages(session.SessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var blocks []codeBlock
	for _, m := range messages {
		if m.Message.Role != "assistant" {
			continue
		}
		for _, b := range m.Message.ContentBlocks() {
			if b.Type != "tool_use" || (b.Name != "Write" && b.Name != "Edit") {
				continue
			}
			var input struct {
				FilePath  string `json:"file_path"`
				Content   string `json:"content"`
				OldString string `json:"old_string"`
				NewString string `json:"new_string"`
			}
			if json.Unmarshal(b.Input, &input) != nil || input.FilePath == "" {
				continue
			}
			cb := codeBlock{
				Tool:      b.Name,
				FilePath:  input.FilePath,
				Timestamp: m.Timestamp,
			}
			if b.Name == "Write" {
				cb.Content = input.Content
			} else {
				cb.Content = input.NewString
				cb.OldString = input.OldString
			}
			blocks = append(blocks, cb)
		}
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	renderTemplate(w, h.tmpl, "conversation_code.html", map[string]any{
		"Title":         title + " - Code",
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "code",
		"HasCodeBlocks": true,
		"Blocks":        blocks,
		"TotalBlocks":   len(blocks),
	})
}

// hasCodeBlocks returns whether a session has Write or Edit tool calls.
func hasCodeBlocks(s *model.SessionEntry) bool {
	return s.ToolUseCounts["Write"]+s.ToolUseCounts["Edit"] > 0
}

// extractToolDetail returns a short summary of what a tool call did.
func extractToolDetail(b model.ContentBlock) string {
	var input map[string]any
	if json.Unmarshal(b.Input, &input) != nil {
		return ""
	}
	switch b.Name {
	case "Bash":
		if cmd, ok := input["command"].(string); ok {
			return truncate(cmd, 120)
		}
	case "Read":
		if fp, ok := input["file_path"].(string); ok {
			return fp
		}
	case "Write":
		if fp, ok := input["file_path"].(string); ok {
			return fp
		}
	case "Edit":
		if fp, ok := input["file_path"].(string); ok {
			return fp
		}
	case "Glob":
		if pat, ok := input["pattern"].(string); ok {
			return pat
		}
	case "Grep":
		if pat, ok := input["pattern"].(string); ok {
			return pat
		}
	case "Task":
		if desc, ok := input["description"].(string); ok {
			return desc
		}
	default:
		for _, key := range []string{"query", "url", "command", "file_path", "path", "description"} {
			if v, ok := input[key].(string); ok {
				return truncate(v, 120)
			}
		}
	}
	return ""
}

func (h *handlers) fileHistoryList(w http.ResponseWriter, r *http.Request) {
	entries, err := h.store.ListFileHistory()
	if err != nil {
		http.Error(w, "loading file history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderTemplate(w, h.tmpl, "filehistory_list.html", map[string]any{
		"Title":       "File History",
		"CurrentPath": "/file-history/",
		"Entries":     entries,
	})
}

func (h *handlers) fileHistoryDetail(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversationId")

	entry, detail, err := h.store.GetFileHistory(conversationID)
	if err != nil || detail == nil {
		http.NotFound(w, r)
		return
	}

	type VersionEntry struct {
		model.FileVersionInfo
		DiffHTML template.HTML
	}
	type HashGroup struct {
		Hash     string
		Versions []VersionEntry
	}
	var groups []HashGroup
	groupMap := make(map[string]int)

	for _, f := range detail.Files {
		ve := VersionEntry{FileVersionInfo: f}
		if idx, ok := groupMap[f.Hash]; ok {
			prev := groups[idx].Versions[len(groups[idx].Versions)-1]
			ve.DiffHTML = renderDiff(prev.Content, f.Content)
			groups[idx].Versions = append(groups[idx].Versions, ve)
		} else {
			groupMap[f.Hash] = len(groups)
			groups = append(groups, HashGroup{
				Hash:     f.Hash,
				Versions: []VersionEntry{ve},
			})
		}
	}

	renderTemplate(w, h.tmpl, "filehistory_detail.html", map[string]any{
		"Title":          "File History: " + conversationID,
		"CurrentPath":    "/file-history/",
		"Entry":          entry,
		"ConversationID": conversationID,
		"Groups":         groups,
		"TotalFiles":     len(detail.Files),
	})
}

const maxSearchResults = 100

type searchResult struct {
	ProjectDirName string
	ProjectDisplay string
	SessionID      string
	SessionPrompt  string
	Role           string
	Timestamp      string
	Snippet        string
}

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var results []searchResult
	if query != "" {
		storeResults, err := h.store.Search(query, maxSearchResults)
		if err != nil {
			// FTS query syntax error — fall back to no results
			storeResults = nil
		}
		for _, sr := range storeResults {
			results = append(results, searchResult{
				ProjectDirName: sr.ProjectDirName,
				ProjectDisplay: sr.ProjectDisplay,
				SessionID:      sr.SessionID,
				SessionPrompt:  sr.SessionPrompt,
				Role:           sr.Role,
				Timestamp:      sr.Timestamp,
				Snippet:        sr.Snippet,
			})
		}
	}

	renderTemplate(w, h.tmpl, "search.html", map[string]any{
		"Title":       "Search",
		"CurrentPath": "/search/",
		"Query":       query,
		"Results":     results,
		"ResultCount": len(results),
		"Capped":      len(results) >= maxSearchResults,
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

func (h *handlers) sessionCompare(w http.ResponseWriter, r *http.Request) {
	dirName := r.PathValue("dirName")

	project, err := h.store.GetProject(dirName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	aID := r.URL.Query().Get("a")
	bID := r.URL.Query().Get("b")
	if aID == "" || bID == "" {
		http.Error(w, "both ?a and ?b session IDs are required", http.StatusBadRequest)
		return
	}

	var sessionA, sessionB *model.SessionEntry
	for i := range project.Sessions {
		sid := project.Sessions[i].SessionID
		if sid == aID {
			sessionA = &project.Sessions[i]
		}
		if sid == bID {
			sessionB = &project.Sessions[i]
		}
	}
	if sessionA == nil || sessionB == nil {
		http.NotFound(w, r)
		return
	}

	toolNames := make(map[string]bool)
	for name := range sessionA.ToolUseCounts {
		toolNames[name] = true
	}
	for name := range sessionB.ToolUseCounts {
		toolNames[name] = true
	}
	type toolCompare struct {
		Name   string
		CountA int
		CountB int
	}
	var tools []toolCompare
	for name := range toolNames {
		tools = append(tools, toolCompare{
			Name:   name,
			CountA: sessionA.ToolUseCounts[name],
			CountB: sessionB.ToolUseCounts[name],
		})
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].CountA+tools[i].CountB > tools[j].CountA+tools[j].CountB
	})

	renderTemplate(w, h.tmpl, "session_compare.html", map[string]any{
		"Title":       "Compare Sessions",
		"CurrentPath": "/projects/",
		"Project":     project,
		"SessionA":    sessionA,
		"SessionB":    sessionB,
		"Tools":       tools,
	})
}
