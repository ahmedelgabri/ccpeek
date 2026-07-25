// Package api serves /api/v1: versioned JSON over the query service,
// mirroring the `ccpeek query` CLI 1:1 (docs/v2-plan.md §5.7). The web
// UI is this API's first client, which keeps the agent-facing surface
// complete by construction. Local-only, like everything else.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
		{"GET /api/v1/history", "history", "op"},
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
		"GET /api/v1/history":                             h.history,
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
	p := newParams(r)
	f := query.SessionsFilter{
		Agent:   p.Str("agent"),
		Project: p.Str("project"),
		Model:   p.Str("model"),
		Since:   p.Date("since"),
		Until:   p.Date("until"),
		Query:   p.Str("q"),
		Limit:   p.Int("limit"),
		Offset:  p.Int("offset"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	sessions, err := h.svc.Sessions(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(sessions))
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
	p := newParams(r)
	opts := query.TranscriptOptions{
		FromSeq: p.Int("from"),
		Limit:   p.Int("limit"),
		Full:    p.Bool("full"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
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
	writeJSON(w, http.StatusOK, orEmpty(msgs))
}

func (h *handlers) sessionTools(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := query.ToolsFilter{
		Limit: p.Int("limit"), Offset: p.Int("offset"),
		FromSeq: p.Int("from_seq"), ToSeq: p.Int("to_seq"),
		Compact: p.Bool("compact"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	tools, err := h.svc.SessionTools(r.Context(), r.PathValue("agent"), r.PathValue("id"), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(tools))
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
	p := newParams(r)
	f := query.CommandsFilter{
		Agent:   p.Str("agent"),
		Project: p.Str("project"),
		Query:   p.Str("q"),
		Since:   p.Date("since"),
		Until:   p.Date("until"),
		Limit:   p.Int("limit"),
		Offset:  p.Int("offset"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
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
	writeJSON(w, http.StatusOK, orEmpty(rows))
}

func (h *handlers) history(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := query.HistoryFilter{
		Agent:  p.Str("agent"),
		Query:  p.Str("q"),
		Limit:  p.Int("limit"),
		Offset: p.Int("offset"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	rows, err := h.svc.History(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(rows))
}

func (h *handlers) usage(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := query.UsageFilter{
		GroupBy: p.Str("group"),
		Agent:   p.Str("agent"),
		Model:   p.Str("model"),
		Since:   p.Date("since"),
		Until:   p.Date("until"),
		Limit:   p.Int("limit"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	rows, err := h.svc.Usage(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(rows))
}

func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := query.SearchFilter{
		Agent: p.Str("agent"),
		Limit: p.Int("limit"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	hits, err := h.svc.Search(r.Context(), p.Str("q"), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(hits))
}

// writeEnvelope is the ONE place a response envelope reaches the wire.
// It was hand-rolled in four places and the fourth copy (the cross-origin
// 403) had already lost its Content-Type header.
func writeEnvelope(w http.ResponseWriter, status int, e envelope) {
	e.Schema = payloadSchema
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	writeEnvelope(w, status, envelope{Data: data})
}

// writeError maps domain errors onto statuses: caller mistakes
// (query.ErrBadRequest) are 400, misses are 404, everything else is a
// 500 — a canceled request or store failure must never masquerade as a
// caller mistake in the access log.
//
// Only the two CALLER-FACING classes carry their message to the client.
// The wrapped errors are informative by design ("listing sessions: …",
// "opening /Users/…/ccpeek2.db: …"), so returning them verbatim on a 500
// handed the caller SQL fragments and absolute filesystem paths. Those go
// to the operator's log instead.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	detail := "internal error"
	switch {
	case errors.Is(err, query.ErrBadRequest):
		status, detail = http.StatusBadRequest, err.Error()
	case errors.Is(err, query.ErrNotFound):
		status, detail = http.StatusNotFound, err.Error()
	case errors.Is(err, context.Canceled):
		// The client went away mid-query; nothing went wrong here.
		status, detail = 499, "request canceled"
	default:
		log.Printf("api error: %v", err)
	}
	writeEnvelope(w, status, envelope{Error: detail})
}

// maxRequestBody bounds the mutating endpoints' payloads. Both carry a
// single field; anything larger is a mistake or an attempt to make the
// server allocate. ReadTimeout bounds the duration but not the memory.
const maxRequestBody = 4 << 10

// decodeBody reads a bounded, strict JSON body: unknown fields are a 400
// rather than a silent no-op, so a client typo surfaces instead of being
// swallowed.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %v", query.ErrBadRequest, err)
	}
	return nil
}

// writeBadRequest is distinct from writeError on purpose: some callers
// (format validation, path-segment parsing) produce bare errors that
// writeError would classify as a 500.
func writeBadRequest(w http.ResponseWriter, err error) {
	writeEnvelope(w, http.StatusBadRequest, envelope{Error: err.Error()})
}
