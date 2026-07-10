// Package api serves /api/v1: versioned JSON over the query service,
// mirroring the `ccpeek query` CLI 1:1 (docs/v2-plan.md §5.7). The v2 web
// UI is this API's first client, which keeps the agent-facing surface
// complete by construction. Local-only, like everything else.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// payloadSchema versions every response envelope.
const payloadSchema = "ccpeek/v1"

type envelope struct {
	Schema string `json:"schema"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Handler mounts the API routes.
func Handler(svc *query.Service) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{svc: svc}
	mux.HandleFunc("GET /api/v1/health", h.health)
	mux.HandleFunc("GET /api/v1/sessions", h.sessions)
	mux.HandleFunc("GET /api/v1/sessions/{agent}/{id}", h.session)
	mux.HandleFunc("GET /api/v1/sessions/{agent}/{id}/transcript", h.transcript)
	mux.HandleFunc("GET /api/v1/usage", h.usage)
	mux.HandleFunc("GET /api/v1/search", h.search)
	return mux
}

type handlers struct {
	svc *query.Service
}

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handlers) sessions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := query.SessionsFilter{
		Agent:   q.Get("agent"),
		Project: q.Get("project"),
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
	writeJSON(w, http.StatusOK, msgs)
}

func (h *handlers) usage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := query.UsageFilter{
		GroupBy: q.Get("group"),
		Agent:   q.Get("agent"),
		Since:   q.Get("since"),
		Until:   q.Get("until"),
		Limit:   intParam(q.Get("limit")),
	}
	rows, err := h.svc.Usage(r.Context(), f)
	if err != nil {
		writeBadRequest(w, err)
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
