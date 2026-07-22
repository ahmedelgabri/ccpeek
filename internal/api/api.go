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

// V1ImportStatus is the legacy-database import outcome, surfaced
// through /api/v1/health so a failed import stays visible instead of
// dissolving into a startup log line. State is "" (no attempt yet),
// "success", "failed" (Error set), or "no-legacy-db".
type V1ImportStatus struct {
	State      string `json:"state"`
	Error      string `json:"error,omitempty"`
	ImportedAt string `json:"importedAt,omitempty"`
}

// Route classifies one API endpoint for the transport-parity test:
// Op names the registry operation answering the same read (HTTP keeps
// hand-written parsing for transport concerns, but the read itself must
// exist in the registry so CLI and MCP can never lack it); "transport"
// marks endpoints that describe this server process or need HTTP
// semantics (health, readiness, SSE, raw bytes with CSP); "write" marks
// mutations, which the read-op registry deliberately excludes.
type Route struct {
	Pattern string
	Op      string // registry op name, or "" when Kind != "op"
	Kind    string // "op" | "transport" | "write"
}

// Routes is the complete classified endpoint table — Handler registers
// exactly this set, so adding an endpoint without classifying it is
// impossible, and the parity test cross-checks the op column against
// the registry.
func Routes() []Route {
	return []Route{
		{"GET /api/v1/health", "", "transport"},
		{"GET /api/v1/ready", "", "transport"},
		{"GET /api/v1/events", "", "transport"},
		{"GET /api/v1/artifacts/{agent}/{kind}/{name}/raw", "", "transport"}, // raw bytes + CSP sandbox
		{"GET /api/v1/stats", "stats", "op"},
		{"GET /api/v1/sessions", "sessions", "op"},
		{"GET /api/v1/sessions/{agent}/{id}", "session", "op"},
		{"GET /api/v1/sessions/{agent}/{id}/transcript", "transcript", "op"},
		{"GET /api/v1/sessions/{agent}/{id}/tools", "tools", "op"},
		{"GET /api/v1/sessions/{agent}/{id}/tools/{seq}", "tool", "op"},
		{"GET /api/v1/commands", "commands", "op"},
		{"GET /api/v1/usage", "usage", "op"},
		{"GET /api/v1/search", "search", "op"},
		{"GET /api/v1/artifacts", "artifacts", "op"},
		{"GET /api/v1/artifacts/{agent}/{kind}/{name}", "artifact", "op"},
		{"GET /api/v1/scan", "scan", "op"},
		{"GET /api/v1/blocks", "blocks", "op"},
		{"GET /api/v1/budget", "budget", "op"},
		{"POST /api/v1/scan/{id}/ignore", "", "write"},
		{"PUT /api/v1/budget", "", "write"},
	}
}

// Handler mounts the API routes. events may be nil when live updates are
// not running (e.g. `--watch` off); the endpoint then answers 501. ready
// reports whether the initial index pass has completed — nil means
// "always ready"; while false, /api/v1/ready answers 503 so scripts (and
// the e2e web server wait) can block on first data. progress, when
// non-nil, feeds the index-pass state into the health payload, and
// v1Import the legacy-import outcome.
func Handler(svc *query.Service, events *Broadcaster, ready func() bool, progress func() IndexProgress, v1Import func() V1ImportStatus) http.Handler {
	mux := http.NewServeMux()
	h := &handlers{svc: svc, events_: events, ready: ready, progress: progress, v1Import: v1Import}
	byPattern := map[string]http.HandlerFunc{
		"GET /api/v1/health":                              h.health,
		"GET /api/v1/ready":                               h.readiness,
		"GET /api/v1/stats":                               h.stats,
		"GET /api/v1/sessions":                            h.sessions,
		"GET /api/v1/sessions/{agent}/{id}":               h.session,
		"GET /api/v1/sessions/{agent}/{id}/transcript":    h.transcript,
		"GET /api/v1/sessions/{agent}/{id}/tools":         h.sessionTools,
		"GET /api/v1/sessions/{agent}/{id}/tools/{seq}":   h.sessionTool,
		"GET /api/v1/commands":                            h.commands,
		"GET /api/v1/usage":                               h.usage,
		"GET /api/v1/search":                              h.search,
		"GET /api/v1/events":                              h.events,
		"GET /api/v1/artifacts":                           h.artifacts,
		"GET /api/v1/artifacts/{agent}/{kind}/{name}":     h.artifact,
		"GET /api/v1/artifacts/{agent}/{kind}/{name}/raw": h.artifactRaw,
		"GET /api/v1/scan":                                h.scanFindings,
		"POST /api/v1/scan/{id}/ignore":                   sameOriginOnly(h.scanIgnore),
		"GET /api/v1/blocks":                              h.blocks,
		"GET /api/v1/budget":                              h.budget,
		"PUT /api/v1/budget":                              sameOriginOnly(h.setBudget),
	}
	for _, r := range Routes() {
		fn, ok := byPattern[r.Pattern]
		if !ok {
			panic("api: route " + r.Pattern + " classified but not implemented")
		}
		mux.HandleFunc(r.Pattern, fn)
		delete(byPattern, r.Pattern)
	}
	if len(byPattern) > 0 {
		panic("api: unclassified routes exist — add them to Routes()")
	}
	return mux
}

type handlers struct {
	svc      *query.Service
	events_  *Broadcaster
	ready    func() bool
	progress func() IndexProgress
	v1Import func() V1ImportStatus
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
	if h.v1Import != nil {
		if st := h.v1Import(); st.State != "" {
			payload["v1Import"] = st
		}
	}
	writeJSON(w, http.StatusOK, payload)
}

// readiness answers 200 once the initial index pass has completed and 503
// before that — the server itself is up the whole time (queries answer
// from whatever is already indexed). A failed v1 import also holds
// readiness at 503 ("v1-import-failed"): the index is genuinely
// incomplete without the legacy data, and anything blocking on this
// endpoint would otherwise read partial history as ready. Health keeps
// answering 200 with the failure detail throughout.
func (h *handlers) readiness(w http.ResponseWriter, r *http.Request) {
	if !h.isReady() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "indexing"})
		return
	}
	if h.v1Import != nil && h.v1Import().State == "failed" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "v1-import-failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
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
	q := r.URL.Query()
	tools, err := h.svc.SessionTools(r.Context(), r.PathValue("agent"), r.PathValue("id"),
		query.ToolsFilter{
			Limit: intParam(q.Get("limit")), Offset: intParam(q.Get("offset")),
			FromSeq: intParam(q.Get("from_seq")), ToSeq: intParam(q.Get("to_seq")),
			Compact: q.Get("compact") != "",
		})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tools)
}

// sessionTool serves one call's full payload including diff excerpts —
// the lazy counterpart the UI requests only when a row expands.
func (h *handlers) sessionTool(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.Atoi(r.PathValue("seq"))
	if err != nil {
		writeBadRequest(w, fmt.Errorf("%w: invalid tool seq %q", query.ErrBadRequest, r.PathValue("seq")))
		return
	}
	detail, err := h.svc.SessionToolDetail(r.Context(), r.PathValue("agent"), r.PathValue("id"), seq)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
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
