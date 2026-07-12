// Package api serves /api/v1: versioned JSON over the query service,
// mirroring the `ccpeek query` CLI 1:1 (docs/v2-plan.md §5.7). The web
// UI is this API's first client, which keeps the agent-facing surface
// complete by construction. Local-only, like everything else.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// payloadSchema versions every response envelope.
const payloadSchema = "ccpeek/v1"

type envelope struct {
	Schema string `json:"schema"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
}

// IndexProgress is a live snapshot of the initial index pass, surfaced
// through /api/v1/health while /api/v1/ready still answers 503, so the
// UI can show real progress instead of a static banner.
type IndexProgress struct {
	Agent   string `json:"agent"`
	Seen    int    `json:"seen"`
	Changed int    `json:"changed"`
}

// Handler mounts the API routes. events may be nil when live updates are
// not running (e.g. `--watch` off); the endpoint then answers 501. ready
// reports whether the initial index pass has completed — nil means
// "always ready"; while false, /api/v1/ready answers 503 so scripts (and
// the e2e web server wait) can block on first data. progress, when
// non-nil, feeds the index-pass state into the health payload.
func Handler(svc *query.Service, events *Broadcaster, ready func() bool, progress func() IndexProgress) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{svc: svc, events_: events, ready: ready, progress: progress}
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("GET /api/v1/ready", h.readiness)
	mux.HandleFunc("GET /api/v1/stats", h.stats)
	mux.HandleFunc("GET /api/v1/sessions", h.sessions)
	mux.HandleFunc("GET /api/v1/sessions/{agent}/{id}", h.session)
	mux.HandleFunc("GET /api/v1/sessions/{agent}/{id}/transcript", h.transcript)
	mux.HandleFunc("GET /api/v1/sessions/{agent}/{id}/tools", h.sessionTools)
	mux.HandleFunc("GET /api/v1/commands", h.commands)
	mux.HandleFunc("GET /api/v1/usage", h.usage)
	mux.HandleFunc("GET /api/v1/search", h.search)
	mux.HandleFunc("GET /api/v1/events", h.events)
	mux.HandleFunc("GET /api/v1/artifacts", h.artifacts)
	mux.HandleFunc("GET /api/v1/artifacts/{agent}/{kind}/{name}", h.artifact)
	mux.HandleFunc("GET /api/v1/artifacts/{agent}/{kind}/{name}/raw", h.artifactRaw)
	mux.HandleFunc("GET /api/v1/scan", h.scanFindings)
	mux.HandleFunc("POST /api/v1/scan/{id}/ignore", sameOriginOnly(h.scanIgnore))
	mux.HandleFunc("GET /api/v1/blocks", h.blocks)
	mux.HandleFunc("GET /api/v1/budget", h.budget)
	mux.HandleFunc("PUT /api/v1/budget", sameOriginOnly(h.setBudget))
	return mux
}

type handlers struct {
	svc      *query.Service
	events_  *Broadcaster
	ready    func() bool
	progress func() IndexProgress
}

func (h *handlers) isReady() bool {
	return h.ready == nil || h.ready()
}

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	payload := map[string]any{
		"status":   "ok",
		"indexing": !h.isReady(),
	}
	if !h.isReady() && h.progress != nil {
		payload["progress"] = h.progress()
	}
	writeJSON(w, http.StatusOK, payload)
}

// readiness answers 200 once the initial index pass has completed and 503
// before that — the server itself is up the whole time (queries answer
// from whatever is already indexed).
func (h *handlers) readiness(w http.ResponseWriter, r *http.Request) {
	if h.isReady() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "indexing"})
}

func (h *handlers) sessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := query.SessionsFilter{
		Agent:   q.Get("agent"),
		Project: q.Get("project"),
		Model:   q.Get("model"),
		Since:   q.Get("since"),
		Until:   q.Get("until"),
		Query:   q.Get("q"),
		Limit:   intParam(q.Get("limit")),
		Offset:  intParam(q.Get("offset")),
	}
	sessions, err := h.svc.Sessions(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (h *handlers) session(w http.ResponseWriter, r *http.Request) {
	detail, err := h.svc.Session(r.Context(), r.PathValue("agent"), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (h *handlers) transcript(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts := query.TranscriptOptions{
		FromSeq: intParam(q.Get("from")),
		Limit:   intParam(q.Get("limit")),
		Full:    q.Get("full") == "1" || q.Get("full") == "true",
	}
	msgs, err := h.svc.Transcript(r.Context(), r.PathValue("agent"), r.PathValue("id"), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	// The web UI renders prose messages as markdown; tool records and
	// system events stay literal.
	for i := range msgs {
		if msgs[i].Kind == "message" {
			msgs[i].HTML = renderMarkdown(msgs[i].Text)
		}
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (h *handlers) sessionTools(w http.ResponseWriter, r *http.Request) {
	tools, err := h.svc.SessionTools(r.Context(), r.PathValue("agent"), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tools)
}

func (h *handlers) stats(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.Stats(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *handlers) commands(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := query.CommandsFilter{
		Agent:   q.Get("agent"),
		Project: q.Get("project"),
		Query:   q.Get("q"),
		Since:   q.Get("since"),
		Until:   q.Get("until"),
		Limit:   intParam(q.Get("limit")),
		Offset:  intParam(q.Get("offset")),
	}
	rows, err := h.svc.Commands(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}

	// ?format=zsh|bash|fish|plain streams a shell history file instead of
	// JSON — the UI's "export to shell" button.
	if format := q.Get("format"); format != "" {
		if err := model.ValidateCommandFormat(format); err != nil {
			writeBadRequest(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition",
			fmt.Sprintf("attachment; filename=%q", "ccpeek-commands."+format))
		// History files read oldest-first; the list endpoint is newest-first.
		for i := len(rows) - 1; i >= 0; i-- {
			entry := model.CommandEntry{Command: rows[i].Command, Timestamp: rows[i].At}
			if err := model.WriteCommand(w, entry, format); err != nil {
				return
			}
		}
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *handlers) usage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := query.UsageFilter{
		GroupBy: q.Get("group"),
		Agent:   q.Get("agent"),
		Model:   q.Get("model"),
		Since:   q.Get("since"),
		Until:   q.Get("until"),
		Limit:   intParam(q.Get("limit")),
	}
	rows, err := h.svc.Usage(r.Context(), f)
	if err != nil {
		// Only caller mistakes are 400s; a canceled request or store
		// failure must not masquerade as one in the access log.
		if errors.Is(err, query.ErrBadRequest) {
			writeBadRequest(w, err)
		} else {
			writeError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hits, err := h.svc.Search(r.Context(), q.Get("q"), query.SearchFilter{
		Agent: q.Get("agent"),
		Limit: intParam(q.Get("limit")),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Schema: payloadSchema, Data: data})
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, query.ErrNotFound) {
		status = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope{Schema: payloadSchema, Error: err.Error()})
}

func writeBadRequest(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(envelope{Schema: payloadSchema, Error: err.Error()})
}

func intParam(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
