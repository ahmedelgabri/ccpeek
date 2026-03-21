package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

const pageSize = 50

type dashboardCard struct {
	Href     string
	Color    string // tailwind color name used in template
	IconPath string // SVG path d attribute
	Count    int
	Label    string
	Sublabel string
}

func newCard(href, color, iconPath string, count int, label, sublabel string) dashboardCard {
	return dashboardCard{href, color, iconPath, count, label, sublabel}
}

type heatmapDay struct {
	Date  string
	Count int
	Level int // 0-4 intensity
}

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	stats, err := h.store.GetStats(ctx)
	if err != nil {
		log.Printf("dashboard: GetStats failed: %v", err)
		http.Error(w, "Failed to load dashboard stats", http.StatusInternalServerError)
		return
	}

	history, err := h.store.ListHistory(ctx, 50)
	if err != nil {
		log.Printf("dashboard: ListHistory failed: %v", err)
	}

	dayCounts, err := h.store.HistoryDayCounts(ctx)
	if err != nil {
		log.Printf("dashboard: HistoryDayCounts failed: %v", err)
	}

	heatmap := buildHeatmapFromCounts(dayCounts)

	toolStats, err := h.store.GetToolUsageStats(ctx, 15)
	if err != nil {
		log.Printf("dashboard: GetToolUsageStats failed: %v", err)
	}

	tokenTimeline, err := h.store.GetTokenTimeline(ctx)
	if err != nil {
		log.Printf("dashboard: GetTokenTimeline failed: %v", err)
	}

	cards := []dashboardCard{
		newCard("/projects/", "sky", "M2.25 12.75V12A2.25 2.25 0 014.5 9.75h15A2.25 2.25 0 0121.75 12v.75m-8.69-6.44l-2.12-2.12a1.5 1.5 0 00-1.061-.44H4.5A2.25 2.25 0 002.25 6v12a2.25 2.25 0 002.25 2.25h15A2.25 2.25 0 0021.75 18V9a2.25 2.25 0 00-2.25-2.25h-5.379a1.5 1.5 0 01-1.06-.44z", stats.ProjectCount, "Projects", fmt.Sprintf("%d sessions", stats.SessionCount)),
		newCard("/plans/", "emerald", "M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z", stats.PlanCount, "Plans", "Markdown plan files"),
		newCard("/shell-snapshots/", "amber", "M6.75 7.5l3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0021 18V6a2.25 2.25 0 00-2.25-2.25H5.25A2.25 2.25 0 003 6v12a2.25 2.25 0 002.25 2.25z", stats.SnapshotCount, "Shell Snapshots", "Shell environment captures"),
		newCard("/commands/", "lime", "m6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 18V6a2.25 2.25 0 0 0-2.25-2.25H5.25A2.25 2.25 0 0 0 3 6v12a2.25 2.25 0 0 0 2.25 2.25Z", stats.CommandCount, "Commands", "Bash commands"),
		newCard("/todos/", "rose", "M9 12.75L11.25 15 15 9.75M21 12a9 9 0 11-18 0 9 9 0 0118 0z", stats.TodoCount, "Todos", "Task lists"),
		newCard("/tasks/", "indigo", "M3.75 12h16.5m-16.5 3.75h16.5M3.75 19.5h16.5M5.625 4.5h12.75a1.875 1.875 0 010 3.75H5.625a1.875 1.875 0 010-3.75z", stats.TaskGroupCount, "Tasks", "Task groups"),
		newCard("/file-history/", "teal", "M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z", stats.FileHistCount, "File History", "File backups"),
		newCard("/paste-cache/", "orange", "M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9.75a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184", stats.PasteCacheCount, "Paste Cache", "Pasted content"),
		newCard("/usage-data/", "fuchsia", "M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z", stats.UsageFacetCount, "Usage Data", "Session insights"),
		newCard("/memories/", "cyan", "M12 18v-5.25m0 0a6.01 6.01 0 001.5-.189m-1.5.189a6.01 6.01 0 01-1.5-.189m3.75 7.478a12.06 12.06 0 01-4.5 0m3.75 2.383a14.406 14.406 0 01-3 0M14.25 18v-.192c0-.983.658-1.823 1.508-2.316a7.5 7.5 0 10-7.517 0c.85.493 1.509 1.333 1.509 2.316V18", stats.MemoryCount, "Memories", "Project context"),
		newCard("/scan/", "red", "M9 12.75 11.25 15 15 9.75m-3-7.036A11.959 11.959 0 0 1 3.598 6 11.99 11.99 0 0 0 3 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285Z", stats.ScanFindingCount, "Secret Scan", "Potential leaks detected"),
	}

	renderTemplate(w, h.tmpl, "dashboard.html", map[string]any{
		"Title":         "Dashboard",
		"CurrentPath":   "/",
		"Cards":         cards,
		"RecentHistory": history,
		"Heatmap":       heatmap,
		"ToolStats":     toolStats,
		"TokenTimeline": tokenTimeline,
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
	ctx := r.Context()

	plans, err := h.store.ListPlans(ctx)
	if err != nil {
		serverError(w, "load plans", err)
		return
	}
	renderTemplate(w, h.tmpl, "plans_list.html", map[string]any{
		"Title":       "Plans",
		"CurrentPath": "/plans/",
		"Plans":       plans,
	})
}

func (h *handlers) planDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fileName := r.PathValue("fileName")

	entry, content, err := h.store.GetPlan(ctx, fileName)
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
	ctx := r.Context()

	snapshots, err := h.store.ListShellSnapshots(ctx)
	if err != nil {
		serverError(w, "load snapshots", err)
		return
	}
	renderTemplate(w, h.tmpl, "snapshots_list.html", map[string]any{
		"Title":       "Shell Snapshots",
		"CurrentPath": "/shell-snapshots/",
		"Snapshots":   snapshots,
	})
}

func (h *handlers) snapshotDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fileName := r.PathValue("fileName")

	entry, content, err := h.store.GetShellSnapshot(ctx, fileName)
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
	ctx := r.Context()

	todos, err := h.store.ListTodos(ctx)
	if err != nil {
		serverError(w, "load todos", err)
		return
	}
	renderTemplate(w, h.tmpl, "todos_list.html", map[string]any{
		"Title":       "Todos",
		"CurrentPath": "/todos/",
		"Todos":       todos,
	})
}

func (h *handlers) todoDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fileName := r.PathValue("fileName")

	entry, items, err := h.store.GetTodo(ctx, fileName)
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
	ctx := r.Context()

	projects, err := h.store.ListProjects(ctx)
	if err != nil {
		serverError(w, "load projects", err)
		return
	}
	renderTemplate(w, h.tmpl, "projects_list.html", map[string]any{
		"Title":       "Projects",
		"CurrentPath": "/projects/",
		"Projects":    projects,
	})
}

func (h *handlers) sessionsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dirName := r.PathValue("dirName")

	projectID, err := h.store.GetProjectID(ctx, dirName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	q := r.URL.Query()
	filter := store.SessionFilter{
		Sort: q.Get("sort"),
		From: q.Get("from"),
		To:   q.Get("to"),
	}

	sessions, err := h.store.ListSessionsFiltered(ctx, projectID, filter)
	if err != nil {
		serverError(w, "load sessions", err)
		return
	}

	projectStats, _ := h.store.GetProjectStats(ctx, projectID)

	project, err := h.store.GetProject(ctx, dirName)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	project.Sessions = sessions
	project.SessionCount = len(sessions)

	renderTemplate(w, h.tmpl, "sessions_list.html", map[string]any{
		"Title":        project.DisplayName,
		"CurrentPath":  "/projects/",
		"Project":      project,
		"ProjectStats": projectStats,
		"Sort":         filter.Sort,
		"From":         filter.From,
		"To":           filter.To,
	})
}

// lookupSession finds a project and session from the URL path values.
func (h *handlers) lookupSession(w http.ResponseWriter, r *http.Request) (*model.ProjectEntry, *model.SessionEntry, bool) {
	ctx := r.Context()
	dirName := r.PathValue("dirName")
	sessionID := r.PathValue("sessionId")

	project, session, err := h.store.GetSession(ctx, dirName, sessionID)
	if err != nil || session == nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return project, session, true
}

func (h *handlers) conversation(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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
	messages, totalMsgs, err := h.store.GetSessionMessages(ctx, project.DirName, session.SessionID, offset, pageSize)
	if err != nil {
		serverError(w, "load messages", err)
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

	headerSummary := fmt.Sprintf("%d messages", totalMsgs)
	if session.EstimatedTokens > 0 {
		headerSummary += fmt.Sprintf(" · ~%s tokens", formatTokens(session.EstimatedTokens))
	}
	renderTemplate(w, h.tmpl, "conversation.html", map[string]any{
		"Title":         title,
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "conversation",
		"HasCodeBlocks": hasCodeBlocks(session),
		"HeaderSummary": headerSummary,
		"ShowExport":    true,
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
	ctx := r.Context()

	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if session.TodoFileName == "" {
		http.NotFound(w, r)
		return
	}

	_, items, err := h.store.GetTodo(ctx, strings.TrimSuffix(session.TodoFileName, ".json"))
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
		"HeaderSummary": fmt.Sprintf("%d items", len(items)),
		"Items":         items,
	})
}

func (h *handlers) conversationFileHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if !session.HasFileHistory {
		http.NotFound(w, r)
		return
	}

	_, detail, err := h.store.GetFileHistory(ctx, session.SessionID)
	if err != nil || detail == nil {
		http.NotFound(w, r)
		return
	}

	groups := groupFileVersions(detail.Files)

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
		"HeaderSummary": fmt.Sprintf("%d file versions", len(detail.Files)),
		"Groups":        groups,
		"TotalFiles":    len(detail.Files),
	})
}

type bashCommand struct {
	Command   string
	Timestamp string
}

func (h *handlers) conversationCommands(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	commands, err := h.store.GetSessionCommands(ctx, project.DirName, session.SessionID)
	if err != nil || len(commands) == 0 {
		http.NotFound(w, r)
		return
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
		"HeaderSummary": fmt.Sprintf("%d commands", len(commands)),
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
	ctx := r.Context()

	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	calls, err := h.store.GetSessionToolCalls(ctx, project.DirName, session.SessionID)
	if err != nil || len(calls) == 0 {
		http.NotFound(w, r)
		return
	}
	stats, err := h.store.GetSessionToolStats(ctx, project.DirName, session.SessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	totalCalls := 0
	for _, stat := range stats {
		totalCalls += stat.Count
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	toolTimeline, _ := h.store.GetToolTimeline(ctx, project.DirName, session.SessionID)

	renderTemplate(w, h.tmpl, "conversation_tools.html", map[string]any{
		"Title":         title + " - Tools",
		"CurrentPath":   "/projects/",
		"Project":       project,
		"Session":       session,
		"ActiveTab":     "tools",
		"HasCodeBlocks": hasCodeBlocks(session),
		"HeaderSummary": fmt.Sprintf("%d tool calls", totalCalls),
		"Stats":         stats,
		"Calls":         calls,
		"TotalCalls":    totalCalls,
		"ToolTimeline":  toolTimeline,
	})
}

func (h *handlers) conversationExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	messages, err := h.store.GetAllSessionMessages(ctx, project.DirName, session.SessionID)
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
	ctx := r.Context()

	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	blocks, err := h.store.GetSessionCodeOperations(ctx, project.DirName, session.SessionID)
	if err != nil || len(blocks) == 0 {
		http.NotFound(w, r)
		return
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
		"HeaderSummary": fmt.Sprintf("%d code operations", len(blocks)),
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
	ctx := r.Context()

	entries, err := h.store.ListFileHistory(ctx)
	if err != nil {
		serverError(w, "load file history", err)
		return
	}
	renderTemplate(w, h.tmpl, "filehistory_list.html", map[string]any{
		"Title":       "File History",
		"CurrentPath": "/file-history/",
		"Entries":     entries,
	})
}

func (h *handlers) fileHistoryDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conversationID := r.PathValue("conversationId")

	entry, detail, err := h.store.GetFileHistory(ctx, conversationID)
	if err != nil || detail == nil {
		http.NotFound(w, r)
		return
	}

	groups := groupFileVersions(detail.Files)

	renderTemplate(w, h.tmpl, "filehistory_detail.html", map[string]any{
		"Title":          "File History: " + conversationID,
		"CurrentPath":    "/file-history/",
		"Entry":          entry,
		"ConversationID": conversationID,
		"Groups":         groups,
		"TotalFiles":     len(detail.Files),
	})
}

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

func (h *handlers) tasksList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	groups, err := h.store.ListTaskGroups(ctx)
	if err != nil {
		serverError(w, "load tasks", err)
		return
	}
	renderTemplate(w, h.tmpl, "tasks_list.html", map[string]any{
		"Title":       "Tasks",
		"CurrentPath": "/tasks/",
		"Groups":      groups,
	})
}

func (h *handlers) taskDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dirName := r.PathValue("dirName")

	entry, items, err := h.store.GetTaskGroup(ctx, dirName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "task_detail.html", map[string]any{
		"Title":       "Task Group",
		"CurrentPath": "/tasks/",
		"Task":        entry,
		"Items":       items,
	})
}

func (h *handlers) pasteCacheList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	entries, err := h.store.ListPasteCache(ctx)
	if err != nil {
		serverError(w, "load paste cache", err)
		return
	}
	renderTemplate(w, h.tmpl, "pastecache_list.html", map[string]any{
		"Title":       "Paste Cache",
		"CurrentPath": "/paste-cache/",
		"Entries":     entries,
	})
}

func (h *handlers) pasteCacheDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fileName := r.PathValue("fileName")

	entry, content, err := h.store.GetPasteCache(ctx, fileName)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "pastecache_detail.html", map[string]any{
		"Title":       entry.FileName,
		"CurrentPath": "/paste-cache/",
		"Entry":       entry,
		"Content":     wrapCode(content, "text"),
	})
}

func (h *handlers) usageDataList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	facets, err := h.store.ListUsageFacets(ctx)
	if err != nil {
		serverError(w, "load usage data", err)
		return
	}
	renderTemplate(w, h.tmpl, "usagedata_list.html", map[string]any{
		"Title":       "Usage Data",
		"CurrentPath": "/usage-data/",
		"Facets":      facets,
	})
}

func (h *handlers) usageDataDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sessionID := r.PathValue("sessionId")

	facet, err := h.store.GetUsageFacet(ctx, sessionID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "usagedata_detail.html", map[string]any{
		"Title":       "Usage: " + truncate(facet.BriefSummary, 60),
		"CurrentPath": "/usage-data/",
		"Facet":       facet,
	})
}

func (h *handlers) usageDataReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	content, err := h.store.GetUsageReport(ctx)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "usagedata_report.html", map[string]any{
		"Title":       "Usage Report",
		"CurrentPath": "/usage-data/",
		"Content":     content,
	})
}

// versionEntry is a file version with an optional diff.
type versionEntry struct {
	model.FileVersionInfo
	DiffHTML template.HTML
}

// hashGroup groups file versions that share the same content hash.
type hashGroup struct {
	Hash     string
	Versions []versionEntry
}

// groupFileVersions groups file versions by hash and computes diffs.
func groupFileVersions(files []model.FileVersionInfo) []hashGroup {
	var groups []hashGroup
	groupMap := make(map[string]int)

	for _, f := range files {
		ve := versionEntry{FileVersionInfo: f}
		if idx, ok := groupMap[f.Hash]; ok {
			prev := groups[idx].Versions[len(groups[idx].Versions)-1]
			ve.DiffHTML = renderDiff(prev.Content, f.Content)
			groups[idx].Versions = append(groups[idx].Versions, ve)
		} else {
			groupMap[f.Hash] = len(groups)
			groups = append(groups, hashGroup{
				Hash:     f.Hash,
				Versions: []versionEntry{ve},
			})
		}
	}
	return groups
}

// usageReportRaw serves the raw HTML for the iframe src.
// A strict CSP sandbox prevents any script execution in the rendered content.
func (h *handlers) usageReportRaw(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	content, err := h.store.GetUsageReport(ctx)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(content))
}

func (h *handlers) memoriesList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	entries, err := h.store.ListMemories(ctx)
	if err != nil {
		serverError(w, "load memories", err)
		return
	}
	renderTemplate(w, h.tmpl, "memories_list.html", map[string]any{
		"Title":       "Memories",
		"CurrentPath": "/memories/",
		"Entries":     entries,
	})
}

func (h *handlers) memoryDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	projectDir := r.PathValue("projectDir")

	entry, content, err := h.store.GetMemory(ctx, projectDir)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	name := entry.ProjectName
	if name == "" {
		name = entry.ProjectDir
	}

	renderTemplate(w, h.tmpl, "memory_detail.html", map[string]any{
		"Title":       "Memory: " + name,
		"CurrentPath": "/memories/",
		"Entry":       entry,
		"Content":     renderMarkdown(content),
	})
}

func (h *handlers) commandsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	q := r.URL.Query()
	filter := store.CommandFilter{
		Project: q.Get("project"),
		Search:  q.Get("search"),
		From:    q.Get("from"),
		To:      q.Get("to"),
	}

	page := 1
	if p := q.Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	offset := (page - 1) * pageSize
	commands, total, err := h.store.ListCommands(ctx, pageSize, offset, filter)
	if err != nil {
		serverError(w, "load commands", err)
		return
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}

	projects, _ := h.store.ListProjectNames(ctx)

	// Build filter query string for pagination and export links
	filterValues := url.Values{}
	if filter.Project != "" {
		filterValues.Set("project", filter.Project)
	}
	if filter.Search != "" {
		filterValues.Set("search", filter.Search)
	}
	if filter.From != "" {
		filterValues.Set("from", filter.From)
	}
	if filter.To != "" {
		filterValues.Set("to", filter.To)
	}
	filterQuery := ""
	if encoded := filterValues.Encode(); encoded != "" {
		filterQuery = "&" + encoded
	}

	renderTemplate(w, h.tmpl, "commands_list.html", map[string]any{
		"Title":       "Commands",
		"CurrentPath": "/commands/",
		"Commands":    commands,
		"Total":       total,
		"Page":        page,
		"TotalPages":  totalPages,
		"HasPrev":     page > 1,
		"HasNext":     page < totalPages,
		"PrevPage":    page - 1,
		"NextPage":    page + 1,
		"Projects":    projects,
		"Project":     filter.Project,
		"Search":      filter.Search,
		"From":        filter.From,
		"To":          filter.To,
		"FilterQuery": filterQuery,
		"Host":        r.Host,
	})
}

func (h *handlers) commandsExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	filter := store.CommandFilter{
		Project: q.Get("project"),
		Search:  q.Get("search"),
		From:    q.Get("from"),
		To:      q.Get("to"),
	}
	format := q.Get("format")
	if format == "" {
		format = "plain"
	}

	commands, err := h.store.ListAllCommands(ctx, filter)
	if err != nil {
		serverError(w, "load commands", err)
		return
	}

	var buf strings.Builder
	_ = model.FormatCommands(&buf, commands, format)

	filename := "commands." + format + ".txt"
	if format == "fish" {
		filename = "commands_fish_history"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Write([]byte(buf.String()))
}

func (h *handlers) sessionCompare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dirName := r.PathValue("dirName")

	project, err := h.store.GetProject(ctx, dirName)
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
		WidthA int
		WidthB int
		Diff   int
	}
	var tools []toolCompare
	for name := range toolNames {
		a := sessionA.ToolUseCounts[name]
		b := sessionB.ToolUseCounts[name]
		maxC := a
		if b > maxC {
			maxC = b
		}
		widthA, widthB := 0, 0
		if maxC > 0 {
			widthA = a * 100 / maxC
			widthB = b * 100 / maxC
		}
		tools = append(tools, toolCompare{
			Name:   name,
			CountA: a,
			CountB: b,
			WidthA: widthA,
			WidthB: widthB,
			Diff:   percentDiff(a, b),
		})
	}
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].CountA+tools[i].CountB > tools[j].CountA+tools[j].CountB
	})

	totalToolsA, totalToolsB := 0, 0
	for _, c := range sessionA.ToolUseCounts {
		totalToolsA += c
	}
	for _, c := range sessionB.ToolUseCounts {
		totalToolsB += c
	}

	renderTemplate(w, h.tmpl, "session_compare.html", map[string]any{
		"Title":        "Compare Sessions",
		"CurrentPath":  "/projects/",
		"Project":      project,
		"SessionA":     sessionA,
		"SessionB":     sessionB,
		"Tools":        tools,
		"TotalToolsA":  totalToolsA,
		"TotalToolsB":  totalToolsB,
		"DurationA":    formatDuration(sessionA.Created, sessionA.Modified),
		"DurationB":    formatDuration(sessionB.Created, sessionB.Modified),
		"DiffMessages": percentDiff(sessionA.MessageCount, sessionB.MessageCount),
		"DiffTokens":   percentDiff(sessionA.EstimatedTokens, sessionB.EstimatedTokens),
		"DiffCommands": percentDiff(sessionA.BashCommandCount, sessionB.BashCommandCount),
		"DiffTools":    percentDiff(totalToolsA, totalToolsB),
	})
}

func (h *handlers) scanList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

	q := r.URL.Query()
	ruleFilter := q.Get("rule")
	typeFilter := q.Get("type")
	showIgnored := q.Get("show_ignored") == "1"

	findings, err := h.store.ListScanFindings(ctx, ruleFilter, typeFilter, showIgnored)
	if err != nil {
		serverError(w, "load scan findings", err)
		return
	}

	stats, err := h.store.GetScanStats(ctx)
	if err != nil {
		log.Printf("scanList: GetScanStats failed: %v", err)
	}

	renderTemplate(w, h.tmpl, "scan_list.html", map[string]any{
		"Title":       "Secret Scan",
		"CurrentPath": "/scan/",
		"Findings":    findings,
		"Stats":       stats,
		"Rule":        ruleFilter,
		"Type":        typeFilter,
		"ShowIgnored": showIgnored,
	})
}

func (h *handlers) scanToggleIgnore(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Basic CSRF check: verify the request originates from this server
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin != "" && !strings.HasPrefix(origin, "http://127.0.0.1") && !strings.HasPrefix(origin, "http://localhost") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.store.ToggleScanFindingIgnored(ctx, id); err != nil {
		serverError(w, "toggle scan finding ignore state", err)
		return
	}

	// Redirect back to the scan page (validate referer is a local path)
	referer := r.Header.Get("Referer")
	if referer == "" || !strings.HasPrefix(referer, "/") {
		referer = "/scan/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}
