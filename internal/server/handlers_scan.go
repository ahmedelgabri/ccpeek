package server

import (
	"log"
	"net/http"
	"strconv"
	"strings"
)

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

	referer := r.Header.Get("Referer")
	if referer == "" || !strings.HasPrefix(referer, "/") {
		referer = "/scan/"
	}
	http.Redirect(w, r, referer, http.StatusSeeOther)
}
