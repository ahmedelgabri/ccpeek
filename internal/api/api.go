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
	"slices"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/ops"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

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
//
// Extra completes an op route's accepted query parameters: the registry
// op declares the read's own inputs, and Extra declares the transport-only
// ones this endpoint additionally reads (a response format, say). The two
// lists together are the WHOLE allowlist — every other parameter is a 400
// — so an accepted name is always a declared one, never a conditional
// buried in a handler.
type Route struct {
	Pattern string
	Op      string   // registry op name, or "" when Kind != "op"
	Kind    string   // "op" | "transport" | "write"
	Extra   []string // transport-only query parameters, "op" routes only
}

// Routes is the complete classified endpoint table — Handler registers
// exactly this set, so adding an endpoint without classifying it is
// impossible, and the parity test cross-checks the op column against
// the registry.
func Routes() []Route {
	return []Route{
		{"GET /api/v1/health", "", "transport", nil},
		{"GET /api/v1/ready", "", "transport", nil},
		{"GET /api/v1/events", "", "transport", nil},
		{"GET /api/v1/artifacts/{agent}/{kind}/{name}/raw", "", "transport", nil}, // raw bytes + CSP sandbox
		{"GET /api/v1/stats", "stats", "op", nil},
		{"GET /api/v1/sessions", "sessions", "op", nil},
		{"GET /api/v1/sessions/{agent}/{id}", "session", "op", nil},
		{"GET /api/v1/sessions/{agent}/{id}/transcript", "transcript", "op", nil},
		{"GET /api/v1/sessions/{agent}/{id}/tools", "tools", "op", nil},
		{"GET /api/v1/sessions/{agent}/{id}/tools/{seq}", "tool", "op", nil},
		{"GET /api/v1/commands", "commands", "op", []string{"format"}}, // shell-history download
		{"GET /api/v1/history", "history", "op", nil},
		{"GET /api/v1/usage", "usage", "op", nil},
		{"GET /api/v1/search", "search", "op", nil},
		{"GET /api/v1/artifacts", "artifacts", "op", nil},
		{"GET /api/v1/artifacts/kinds", "artifact-kinds", "op", nil},
		{"GET /api/v1/artifacts/{agent}/{kind}/{name}", "artifact", "op", nil},
		{"GET /api/v1/scan", "scan", "op", nil},
		{"GET /api/v1/scan/rules", "scan-rules", "op", nil},
		{"GET /api/v1/blocks", "blocks", "op", nil},
		{"GET /api/v1/budget", "budget", "op", nil},
		{"POST /api/v1/scan/{id}/ignore", "", "write", nil},
		{"PUT /api/v1/budget", "", "write", nil},
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
		"GET /api/v1/artifacts/kinds":                     h.artifactKinds,
		"GET /api/v1/artifacts/{agent}/{kind}/{name}":     h.artifact,
		"GET /api/v1/artifacts/{agent}/{kind}/{name}/raw": h.artifactRaw,
		"GET /api/v1/scan":                                h.scanFindings,
		"GET /api/v1/scan/rules":                          h.scanRules,
		"POST /api/v1/scan/{id}/ignore":                   sameOriginOnly(h.scanIgnore),
		"GET /api/v1/blocks":                              h.blocks,
		"GET /api/v1/budget":                              h.budget,
		"PUT /api/v1/budget":                              sameOriginOnly(h.setBudget),
	}
	byOp := map[string]ops.Op{}
	for _, op := range ops.Registry() {
		byOp[op.Name] = op
	}
	for _, r := range Routes() {
		fn, ok := byPattern[r.Pattern]
		if !ok {
			panic("api: route " + r.Pattern + " classified but not implemented")
		}
		if r.Kind == "op" {
			op, ok := byOp[r.Op]
			if !ok {
				panic("api: route " + r.Pattern + " names unknown registry op " + strconv.Quote(r.Op))
			}
			fn = rejectUnknownParams(AcceptedParams(r, op), fn)
		} else if len(r.Extra) > 0 {
			panic("api: route " + r.Pattern + " declares query parameters but is not an op route")
		}
		mux.HandleFunc(r.Pattern, fn)
		delete(byPattern, r.Pattern)
	}
	if len(byPattern) > 0 {
		panic("api: unclassified routes exist — add them to Routes()")
	}
	return mux
}

// AcceptedParams is the sorted allowlist of query parameters an op route
// takes: the registry op's parameters, minus the ones this pattern binds
// as path segments, plus the route's declared transport-only extras.
// Deriving it from the registry is what makes the canonical names the
// ONLY spelling HTTP answers to — the drift it replaces had /sessions
// reading "q" for the parameter the registry calls "query".
//
// Exported because `ccpeek docs --agents` generates its endpoint list
// from the same source the server enforces, rather than a hand-written
// copy that went stale the moment a parameter was renamed.
func AcceptedParams(r Route, op ops.Op) []string {
	inPath := pathWildcards(r.Pattern)
	names := append([]string(nil), r.Extra...)
	for _, p := range op.Params {
		if !inPath[p.Name] {
			names = append(names, p.Name)
		}
	}
	slices.Sort(names)
	return names
}

// pathWildcards reports the {name} segments a pattern binds: those
// parameters arrive in the path, so they are not query parameters here
// even though the registry declares them (agent and id reach /transcript
// as path segments, not ?agent=).
func pathWildcards(pattern string) map[string]bool {
	out := map[string]bool{}
	for _, seg := range strings.Split(pattern, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out[strings.Trim(seg, "{}")] = true
		}
	}
	return out
}

// rejectUnknownParams answers 400 for any query parameter outside the
// route's allowlist. Ignoring one silently is how a caller that used the
// wrong name got an UNFILTERED result with a 200 — a superset of what it
// asked for, indistinguishable from an answer. The reply names the
// offenders and the accepted set so the caller can correct itself.
func rejectUnknownParams(accepted []string, next http.HandlerFunc) http.HandlerFunc {
	valid := make(map[string]bool, len(accepted))
	for _, name := range accepted {
		valid[name] = true
	}
	expected := "this endpoint takes no query parameters"
	if len(accepted) > 0 {
		expected = "valid: " + strings.Join(accepted, ", ")
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var unknown []string
		for name := range r.URL.Query() {
			if !valid[name] {
				unknown = append(unknown, strconv.Quote(name))
			}
		}
		if len(unknown) > 0 {
			slices.Sort(unknown)
			noun := "parameter"
			if len(unknown) > 1 {
				noun = "parameters"
			}
			writeBadRequest(w, fmt.Errorf("%w: unknown %s %s (%s)",
				query.ErrBadRequest, noun, strings.Join(unknown, ", "), expected))
			return
		}
		next(w, r)
	}
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
	indexing := !h.isReady()
	payload := map[string]any{
		"status":   "ok",
		"indexing": indexing,
	}
	if indexing && h.progress != nil {
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
		Since:   p.Str("since"),
		Until:   p.Str("until"),
		Query:   p.Str("query"),
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
	p := newParams(r)
	opts := query.TranscriptOptions{
		FromSeq: p.Int("from_seq"),
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
	writeJSON(w, http.StatusOK, msgs)
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
	p := newParams(r)
	// format is this route's declared transport-only parameter (see
	// Routes); reading it through params keeps every query parameter this
	// handler consumes visible in one place.
	format := p.Str("format")
	f := query.CommandsFilter{
		Agent:   p.Str("agent"),
		Project: p.Str("project"),
		Query:   p.Str("query"),
		Since:   p.Str("since"),
		Until:   p.Str("until"),
		Limit:   p.Int("limit"),
		Offset:  p.Int("offset"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}

	// ?format=zsh|bash|fish|plain writes a shell history file instead of
	// JSON — the UI's "export to shell" button.
	if format != "" {
		h.exportCommands(w, r, f, format)
		return
	}

	rows, err := h.svc.Commands(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// exportCommands writes the shell-history download.
//
// It pages the op to COMPLETION, the way `ccpeek export commands` does.
// Serving a single page here meant the commands ceiling (1000) was also the
// export's: a larger corpus produced a file missing everything older, with
// no truncation marker anywhere in it — the browser tab's own history
// silently rewritten to end a year ago. An export is a download of a
// selection, not a page of a list view.
//
// An explicitly supplied limit stays the export's bound, including an
// over-cap one: the query layer refuses that rather than truncating, and
// this must not turn the refusal into a quietly clipped file.
func (h *handlers) exportCommands(w http.ResponseWriter, r *http.Request, f query.CommandsFilter, format string) {
	if err := model.ValidateCommandFormat(format); err != nil {
		writeBadRequest(w, err)
		return
	}

	bounded := f.Limit > 0
	if !bounded {
		// Page at the op's own ceiling — the largest page it will answer.
		// A ceiling of 0 would mean "no maximum"; page at a fixed size then,
		// so the loop below always has a page length to compare against.
		f.Limit = query.CommandsLimit.Max
		if f.Limit <= 0 {
			f.Limit = 1000
		}
	}
	var rows []query.CommandRow
	for {
		page, err := h.svc.Commands(r.Context(), f)
		if err != nil {
			// Nothing has been written yet, so a failure mid-export is still
			// a clean status rather than a truncated attachment.
			writeError(w, err)
			return
		}
		rows = append(rows, page...)
		if bounded || len(page) < f.Limit {
			break
		}
		f.Offset += len(page)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "ccpeek-commands."+format))
	// History files read oldest-first while the op answers newest-first, so
	// the WHOLE result is reversed ONCE, here. Reversing each page as it
	// arrived would order commands correctly inside a page and backwards
	// across them — the second page's oldest command landing after the
	// first page's newest.
	for i := len(rows) - 1; i >= 0; i-- {
		entry := model.CommandEntry{Command: rows[i].Command, Timestamp: rows[i].At}
		if err := model.WriteCommand(w, entry, format); err != nil {
			return
		}
	}
}

func (h *handlers) history(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := query.HistoryFilter{
		Agent:  p.Str("agent"),
		Query:  p.Str("query"),
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
	writeJSON(w, http.StatusOK, rows)
}

func (h *handlers) usage(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := query.UsageFilter{
		GroupBy: p.Str("group"),
		Agent:   p.Str("agent"),
		Model:   p.Str("model"),
		Since:   p.Str("since"),
		Until:   p.Str("until"),
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
	writeJSON(w, http.StatusOK, rows)
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
	hits, err := h.svc.Search(r.Context(), p.Str("query"), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hits)
}

// writeEnvelope is the ONE place a response envelope reaches the wire.
// It was hand-rolled in four places and the fourth copy (the cross-origin
// 403) had already lost its Content-Type header.
func writeEnvelope(w http.ResponseWriter, status int, e ops.Envelope) {
	e.Schema = ops.PayloadSchema
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// writeJSON builds its envelope with ops.Wrap, the same constructor the
// CLI and MCP use — that is where a nil list becomes [] for every
// transport at once, so this layer no longer carries its own correction.
func writeJSON(w http.ResponseWriter, status int, data any) {
	writeEnvelope(w, status, ops.Wrap(data))
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
	writeEnvelope(w, status, ops.Envelope{Error: detail})
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
	writeEnvelope(w, http.StatusBadRequest, ops.Envelope{Error: err.Error()})
}
