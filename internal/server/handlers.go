package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccexplore/internal/model"
)

const pageSize = 50

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	idx := h.store.Index

	totalSessions := 0
	for _, p := range idx.Projects {
		totalSessions += p.SessionCount
	}

	history := idx.History
	if len(history) > 50 {
		history = history[:50]
	}

	renderTemplate(w, h.tmpl, "dashboard.html", map[string]any{
		"Title":         "Dashboard",
		"CurrentPath":   "/",
		"Index":         idx,
		"TotalSessions": totalSessions,
		"RecentHistory": history,
	})
}

func (h *handlers) plansList(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, h.tmpl, "plans_list.html", map[string]any{
		"Title":       "Plans",
		"CurrentPath": "/plans/",
		"Plans":       h.store.Index.Plans,
	})
}

func (h *handlers) planDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")

	// Find the plan entry
	var entry *model.PlanEntry
	for i := range h.store.Index.Plans {
		name := strings.TrimSuffix(h.store.Index.Plans[i].FileName, ".md")
		if name == fileName {
			entry = &h.store.Index.Plans[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile(filepath.Join(h.store.DataDir, "plans", entry.FileName))
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
		"Snapshots":   h.store.Index.ShellSnapshots,
	})
}

func (h *handlers) snapshotDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")

	var entry *model.ShellSnapshotEntry
	for i := range h.store.Index.ShellSnapshots {
		name := strings.TrimSuffix(h.store.Index.ShellSnapshots[i].FileName, ".sh")
		if name == fileName {
			entry = &h.store.Index.ShellSnapshots[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	content, err := os.ReadFile(filepath.Join(h.store.DataDir, "shell-snapshots", entry.FileName))
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
		"Todos":       h.store.Index.Todos,
	})
}

func (h *handlers) todoDetail(w http.ResponseWriter, r *http.Request) {
	fileName := r.PathValue("fileName")

	var entry *model.TodoEntry
	for i := range h.store.Index.Todos {
		name := strings.TrimSuffix(h.store.Index.Todos[i].FileName, ".json")
		if name == fileName {
			entry = &h.store.Index.Todos[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(h.store.DataDir, "todos", entry.FileName))
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
		"Projects":    h.store.Index.Projects,
	})
}

func (h *handlers) sessionsList(w http.ResponseWriter, r *http.Request) {
	dirName := r.PathValue("dirName")

	var project *model.ProjectEntry
	for i := range h.store.Index.Projects {
		if h.store.Index.Projects[i].DirName == dirName {
			project = &h.store.Index.Projects[i]
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
// Returns false and writes a 404 if either is not found.
func (h *handlers) lookupSession(w http.ResponseWriter, r *http.Request) (*model.ProjectEntry, *model.SessionEntry, bool) {
	dirName := r.PathValue("dirName")
	sessionID := r.PathValue("sessionId")

	var project *model.ProjectEntry
	for i := range h.store.Index.Projects {
		if h.store.Index.Projects[i].DirName == dirName {
			project = &h.store.Index.Projects[i]
			break
		}
	}
	if project == nil {
		http.NotFound(w, r)
		return nil, nil, false
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
		return nil, nil, false
	}

	return project, session, true
}

func (h *handlers) conversation(w http.ResponseWriter, r *http.Request) {
	project, session, ok := h.lookupSession(w, r)
	if !ok {
		return
	}

	data, err := os.ReadFile(filepath.Join(h.store.DataDir, "projects", project.DirName, session.SessionID+".json"))
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
		"Title":       title,
		"CurrentPath": "/projects/",
		"Project":     project,
		"Session":     session,
		"ActiveTab":   "conversation",
		"Messages":    pageMessages,
		"TotalMsgs":   len(messages),
		"Page":        page,
		"TotalPages":  totalPages,
		"HasPrev":     page > 1,
		"HasNext":     page < totalPages,
		"PrevPage":    page - 1,
		"NextPage":    page + 1,
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

	data, err := os.ReadFile(filepath.Join(h.store.DataDir, "todos", session.TodoFileName))
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
		"Title":       title + " - Todos",
		"CurrentPath": "/projects/",
		"Project":     project,
		"Session":     session,
		"ActiveTab":   "todos",
		"Items":       items,
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

	data, err := os.ReadFile(filepath.Join(h.store.DataDir, "file-history", session.SessionID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var detail model.FileHistoryDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		http.Error(w, "invalid file history data", http.StatusInternalServerError)
		return
	}

	type HashGroup struct {
		Hash     string
		Versions []model.FileVersionInfo
	}
	var groups []HashGroup
	groupMap := make(map[string]int)

	for _, f := range detail.Files {
		if idx, ok := groupMap[f.Hash]; ok {
			groups[idx].Versions = append(groups[idx].Versions, f)
		} else {
			groupMap[f.Hash] = len(groups)
			groups = append(groups, HashGroup{
				Hash:     f.Hash,
				Versions: []model.FileVersionInfo{f},
			})
		}
	}

	title := session.FirstPrompt
	if title == "" {
		title = session.SessionID
	}

	renderTemplate(w, h.tmpl, "conversation_filehistory.html", map[string]any{
		"Title":       title + " - File History",
		"CurrentPath": "/projects/",
		"Project":     project,
		"Session":     session,
		"ActiveTab":   "file-history",
		"Groups":      groups,
		"TotalFiles":  len(detail.Files),
	})
}

func (h *handlers) fileHistoryList(w http.ResponseWriter, r *http.Request) {
	renderTemplate(w, h.tmpl, "filehistory_list.html", map[string]any{
		"Title":       "File History",
		"CurrentPath": "/file-history/",
		"Entries":     h.store.Index.FileHistory,
	})
}

func (h *handlers) fileHistoryDetail(w http.ResponseWriter, r *http.Request) {
	conversationID := r.PathValue("conversationId")

	var entry *model.FileHistoryEntry
	for i := range h.store.Index.FileHistory {
		if h.store.Index.FileHistory[i].ConversationID == conversationID {
			entry = &h.store.Index.FileHistory[i]
			break
		}
	}
	if entry == nil {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(h.store.DataDir, "file-history", conversationID+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var detail model.FileHistoryDetail
	if err := json.Unmarshal(data, &detail); err != nil {
		http.Error(w, "invalid file history data", http.StatusInternalServerError)
		return
	}

	// Group files by hash
	type HashGroup struct {
		Hash     string
		Versions []model.FileVersionInfo
	}
	var groups []HashGroup
	groupMap := make(map[string]int)

	for _, f := range detail.Files {
		if idx, ok := groupMap[f.Hash]; ok {
			groups[idx].Versions = append(groups[idx].Versions, f)
		} else {
			groupMap[f.Hash] = len(groups)
			groups = append(groups, HashGroup{
				Hash:     f.Hash,
				Versions: []model.FileVersionInfo{f},
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
