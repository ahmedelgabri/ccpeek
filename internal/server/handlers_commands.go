package server

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

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
