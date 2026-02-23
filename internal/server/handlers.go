package server

import (
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
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
	idx := h.store.Load().Index

	totalSessions := 0
	for _, p := range idx.Projects {
		totalSessions += p.SessionCount
	}

	history := idx.History
	if len(history) > 50 {
		history = history[:50]
	}

	heatmap := buildHeatmap(idx.History)

	renderTemplate(w, h.tmpl, "dashboard.html", map[string]any{
		"Title":         "Dashboard",
		"CurrentPath":   "/",
		"Index":         idx,
		"TotalSessions": totalSessions,
		"RecentHistory": history,
		"Heatmap":       heatmap,
	})
}

func buildHeatmap(history []model.HistoryEntry) []heatmapDay {
	// Count conversations per day from history timestamps
	dayCounts := make(map[string]int)
	for _, entry := range history {
		t := time.UnixMilli(entry.Timestamp)
		day := t.Format("2006-01-02")
		dayCounts[day]++
	}

	// Build 52 weeks (364 days) of data ending today
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

	// Assign intensity levels
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

func (h *handlers) plansList(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, h.tmpl, "plans_list.html", map[string]any{
		"Title":       "Plans",
		"CurrentPath": "/plans/",
		"Plans":       h.store.Load().Index.Plans,
	})
}

func (h *handlers) planDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")
	store := h.store.Load()

	var entry *model.PlanEntry
	for i := range store.Index.Plans {
		name := strings.TrimSuffix(store.Index.Plans[i].FileName, ".md")
		if name == fileName {
			entry = &store.Index.Plans[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile(filepath.Join(store.DataDir, "plans", entry.FileName))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "plan_detail.html", map[string]any{
		"Title":       entry.Title,
		"CurrentPath": "/plans/",
		"Plan":        entry,
		"Content":     renderMarkdown(string(content)),
	})
}

func (h *handlers) snapshotsList(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, h.tmpl, "snapshots_list.html", map[string]any{
		"Title":       "Shell Snapshots",
		"CurrentPath": "/shell-snapshots/",
		"Snapshots":   h.store.Load().Index.ShellSnapshots,
	})
}

func (h *handlers) snapshotDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")
	store := h.store.Load()

	var entry *model.ShellSnapshotEntry
	for i := range store.Index.ShellSnapshots {
		name := strings.TrimSuffix(store.Index.ShellSnapshots[i].FileName, ".sh")
		if name == fileName {
			entry = &store.Index.ShellSnapshots[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile(filepath.Join(store.DataDir, "shell-snapshots", entry.FileName))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	renderTemplate(w, h.tmpl, "snapshot_detail.html", map[string]any{
		"Title":       entry.FileName,
		"CurrentPath": "/shell-snapshots/",
		"Snapshot":    entry,
		"Content":     wrapCode(string(content), "bash"),
	})
}

func (h *handlers) todosList(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, h.tmpl, "todos_list.html", map[string]any{
		"Title":       "Todos",
		"CurrentPath": "/todos/",
		"Todos":       h.store.Load().Index.Todos,
	})
}

func (h *handlers) todoDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")
	store := h.store.Load()

	var entry *model.TodoEntry
	for i := range store.Index.Todos {
		name := strings.TrimSuffix(store.Index.Todos[i].FileName, ".json")
		if name == fileName {
			entry = &store.Index.Todos[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "todos", entry.FileName))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var items []model.TodoItem
	if err := json.Unmarshal(data, &items); err != nil {
		http.Error(w, "invalid todo data", http.StatusInternalServerError)
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
	renderTemplate(w, h.tmpl, "projects_list.html", map[string]any{
		"Title":       "Projects",
		"CurrentPath": "/projects/",
		"Projects":    h.store.Load().Index.Projects,
	})
}

func (h *handlers) sessionsList(w http.ResponseWriter, r *http.Request) {
	dirName := r.PathValue("dirName")
	store := h.store.Load()

	var project *model.ProjectEntry
	for i := range store.Index.Projects {
		if store.Index.Projects[i].DirName == dirName {
			project = &store.Index.Projects[i]
			break
		}
	}
	if project == nil {
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
// Returns the DataStore snapshot alongside the project/session so callers
// can reuse it without additional atomic loads.
// Returns false and writes a 404 if either is not found.
func (h *handlers) lookupSession(w http.ResponseWriter, r *http.Request) (*DataStore, *model.ProjectEntry, *model.SessionEntry, bool) {
	dirName := r.PathValue("dirName")
	sessionID := r.PathValue("sessionId")

	store := h.store.Load()
	var project *model.ProjectEntry
	for i := range store.Index.Projects {
		if store.Index.Projects[i].DirName == dirName {
			project = &store.Index.Projects[i]
			break
		}
	}
	if project == nil {
		http.NotFound(w, r)
		return nil, nil, nil, false
	}

	var session *model.SessionEntry
	for i := range project.Sessions {
		if project.Sessions[i].SessionID == sessionID {
			session = &project.Sessions[i]
			break
		}
	}
	if session == nil {
		http.NotFound(w, r)
		return nil, nil, nil, false
	}

	return store, project, session, true
}

func (h *handlers) conversation(w http.ResponseWriter, r *http.Request) {
	store, project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "projects", project.DirName, session.SessionID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var messages []model.ConversationMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		http.Error(w, "invalid conversation data", http.StatusInternalServerError)
		return
	}

	// Pagination
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}

	totalPages := (len(messages) + pageSize - 1) / pageSize
	if page > totalPages {
		page = totalPages
	}
	if totalPages == 0 {
		totalPages = 1
	}

	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(messages) {
		end = len(messages)
	}

	pageMessages := messages[start:end]

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
		"Messages":      pageMessages,
		"TotalMsgs":     len(messages),
		"Page":          page,
		"TotalPages":    totalPages,
		"HasPrev":       page > 1,
		"HasNext":       page < totalPages,
		"PrevPage":      page - 1,
		"NextPage":      page + 1,
	})
}

func (h *handlers) conversationTodos(w http.ResponseWriter, r *http.Request) {
	store, project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if session.TodoFileName == "" {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "todos", session.TodoFileName))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var items []model.TodoItem
	if err := json.Unmarshal(data, &items); err != nil {
		http.Error(w, "invalid todo data", http.StatusInternalServerError)
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
	store, project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if !session.HasFileHistory {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "file-history", session.SessionID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var detail model.FileHistoryDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		http.Error(w, "invalid file history data", http.StatusInternalServerError)
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
	store, project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if session.BashCommandCount == 0 {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "projects", project.DirName, session.SessionID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var messages []model.ConversationMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		http.Error(w, "invalid conversation data", http.StatusInternalServerError)
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
	store, project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if len(session.ToolUseCounts) == 0 {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "projects", project.DirName, session.SessionID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var messages []model.ConversationMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		http.Error(w, "invalid conversation data", http.StatusInternalServerError)
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

	// Build sorted stats
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
	store, project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "projects", project.DirName, session.SessionID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var messages []model.ConversationMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		http.Error(w, "invalid conversation data", http.StatusInternalServerError)
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
	Tool      string // "Write" or "Edit"
	FilePath  string
	Content   string // Full content for Write, new_string for Edit
	OldString string // Only for Edit
	Timestamp string
}

func (h *handlers) conversationCode(w http.ResponseWriter, r *http.Request) {
	store, project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	if !hasCodeBlocks(session) {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "projects", project.DirName, session.SessionID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var messages []model.ConversationMessage
	if err := json.Unmarshal(data, &messages); err != nil {
		http.Error(w, "invalid conversation data", http.StatusInternalServerError)
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
		// Generic: try common field names
		for _, key := range []string{"query", "url", "command", "file_path", "path", "description"} {
			if v, ok := input[key].(string); ok {
				return truncate(v, 120)
			}
		}
	}
	return ""
}

func (h *handlers) fileHistoryList(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, h.tmpl, "filehistory_list.html", map[string]any{
		"Title":       "File History",
		"CurrentPath": "/file-history/",
		"Entries":     h.store.Load().Index.FileHistory,
	})
}

func (h *handlers) fileHistoryDetail(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversationId")
	store := h.store.Load()

	var entry *model.FileHistoryEntry
	for i := range store.Index.FileHistory {
		if store.Index.FileHistory[i].ConversationID == conversationID {
			entry = &store.Index.FileHistory[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(store.DataDir, "file-history", conversationID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var detail model.FileHistoryDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		http.Error(w, "invalid file history data", http.StatusInternalServerError)
		return
	}

	// Group files by hash (reuse same VersionEntry/HashGroup as conversationFileHistory)
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
	store := h.store.Load()

	var results []searchResult
	if query != "" {
		queryLower := strings.ToLower(query)
		for _, project := range store.Index.Projects {
			for _, session := range project.Sessions {
				data, err := os.ReadFile(filepath.Join(store.DataDir, "projects", project.DirName, session.SessionID+".json"))
				if err != nil {
					continue
				}
				var messages []model.ConversationMessage
				if json.Unmarshal(data, &messages) != nil {
					continue
				}
				for _, m := range messages {
					text := m.Message.ContentText()
					if text == "" {
						continue
					}
					idx := strings.Index(strings.ToLower(text), queryLower)
					if idx < 0 {
						continue
					}
					snippet := extractSnippet(text, idx, len(query), 120)
					prompt := session.FirstPrompt
					if prompt == "" {
						prompt = session.SessionID
					}
					results = append(results, searchResult{
						ProjectDirName: project.DirName,
						ProjectDisplay: project.DisplayName,
						SessionID:      session.SessionID,
						SessionPrompt:  prompt,
						Role:           m.Message.Role,
						Timestamp:      m.Timestamp,
						Snippet:        snippet,
					})
					if len(results) >= maxSearchResults {
						break
					}
				}
				if len(results) >= maxSearchResults {
					break
				}
			}
			if len(results) >= maxSearchResults {
				break
			}
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
	// Clean up whitespace
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
	store := h.store.Load()

	var project *model.ProjectEntry
	for i := range store.Index.Projects {
		if store.Index.Projects[i].DirName == dirName {
			project = &store.Index.Projects[i]
			break
		}
	}
	if project == nil {
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

	// Merge tool names from both sessions for comparison
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
