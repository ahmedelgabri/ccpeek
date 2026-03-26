package server

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/model"
)

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
		newCard("/memories/", "cyan", "M12 18v-5.25m0 0a6.01 6.01 0 001.5-.189m-1.5.189a6.01 6.01 0 01-1.5-.189m3.75 7.478a12.06 12.06 0 01-4.5 0m3.75 2.383a14.406 14.406 0 01-3 0M14.25 18v-.192c0-.983.658-1.823 1.508-2.316a7.5 7.5 0 10-7.517 0c.85.493 1.509 1.333 1.509 2.316V18", stats.MemoryCount, "Memories", "Markdown context files"),
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
