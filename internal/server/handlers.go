package server

import (
	"html/template"
	"net/http"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

const pageSize = 50

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
