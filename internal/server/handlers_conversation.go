package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

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
	var (
		messages  []model.ConversationMessage
		totalMsgs int
		err       error
	)
	if !session.MetadataOnly {
		messages, totalMsgs, err = h.store.GetSessionMessages(ctx, project.DirName, session.SessionID, offset, pageSize)
		if err != nil {
			serverError(w, "load messages", err)
			return
		}
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
	if session.MetadataOnly {
		headerSummary = "Metadata only session"
	} else if session.EstimatedTokens > 0 {
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
		"ShowExport":    !session.MetadataOnly,
		"MetadataOnly":  session.MetadataOnly,
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

	if session.MetadataOnly {
		var buf strings.Builder
		title := session.FirstPrompt
		if title == "" {
			title = session.SessionID
		}
		buf.WriteString("# " + title + "\n\n")
		buf.WriteString("This session is metadata-only. Full transcript content is not available from this source.\n\n")
		if session.Created != "" {
			buf.WriteString("**Created:** " + session.Created + "\n")
		}
		if session.Modified != "" {
			buf.WriteString("**Updated:** " + session.Modified + "\n")
		}
		if session.ModelName != "" {
			buf.WriteString("**Model:** " + session.ModelName + "\n")
		}
		filename := session.SessionID + ".md"
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
		w.Write([]byte(buf.String()))
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

// hasCodeBlocks returns whether a session has code-writing/editing tool calls.
func hasCodeBlocks(s *model.SessionEntry) bool {
	return s.ToolUseCounts["Write"]+s.ToolUseCounts["Edit"]+s.ToolUseCounts["MultiEdit"] > 0
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
