package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// sameOriginOnly rejects cross-origin browser requests to mutating
// endpoints (CSRF guard, matching v1's toggle-ignore protection). The
// server only binds 127.0.0.1; this blocks malicious websites driving the
// local API through a victim's browser.
func sameOriginOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !isLoopbackHost(u.Hostname()) {
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(envelope{
					Schema: payloadSchema, Error: "cross-origin requests are not allowed",
				})
				return
			}
		}
		next(w, r)
	}
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" ||
		strings.HasSuffix(host, ".localhost")
}

func (h *handlers) artifacts(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	f := query.ArtifactsFilter{
		Agent:  p.Str("agent"),
		Kind:   p.Str("kind"),
		Limit:  p.Int("limit"),
		Offset: p.Int("offset"),
	}
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	list, err := h.svc.Artifacts(r.Context(), f)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(list))
}

func (h *handlers) artifact(w http.ResponseWriter, r *http.Request) {
	detail, err := h.svc.Artifact(r.Context(),
		r.PathValue("agent"), r.PathValue("kind"), r.PathValue("name"),
		renderArtifact)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// artifactRaw serves an artifact's stored bytes verbatim. Agent-produced
// HTML (the Claude usage report) ships as text/html so the UI can host it
// in a sandboxed iframe; everything else is text/plain, never interpreted
// in the app's own origin.
//
// The HTML response carries its own CSP sandbox: the iframe's sandbox
// attribute does not protect a DIRECT navigation to this URL, where the
// stored (agent-produced, untrusted) markup would otherwise run with the
// app origin and could drive mutating endpoints. `sandbox allow-scripts`
// keeps the report's charts working while the document gets an opaque
// origin — its API requests carry "Origin: null" and fail the
// same-origin guard. nosniff stops browsers promoting the text/plain
// kinds to something executable.
func (h *handlers) artifactRaw(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	detail, err := h.svc.Artifact(r.Context(),
		r.PathValue("agent"), kind, r.PathValue("name"), nil)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if kind == "usage_report" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
	} else {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	_, _ = w.Write([]byte(detail.Content))
}

func (h *handlers) scanFindings(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	findings, err := h.svc.ScanFindings(r.Context(), p.Bool("ignored"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(findings))
}

func (h *handlers) scanIgnore(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeBadRequest(w, err)
		return
	}
	var body struct {
		Ignored bool `json:"ignored"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, err)
		return
	}
	if err := h.svc.SetScanIgnore(r.Context(), id, body.Ignored); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ignored": body.Ignored})
}

func (h *handlers) blocks(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	agent, limit := p.Str("agent"), p.Int("limit")
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	blocks, err := h.svc.Blocks(r.Context(), agent, limit)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, orEmpty(blocks))
}

func (h *handlers) budget(w http.ResponseWriter, r *http.Request) {
	b, err := h.svc.GetBudget(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (h *handlers) setBudget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MonthlyUSD float64 `json:"monthlyUSD"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeBadRequest(w, err)
		return
	}
	if err := h.svc.SetBudget(r.Context(), body.MonthlyUSD); err != nil {
		writeBadRequest(w, err)
		return
	}
	b, err := h.svc.GetBudget(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, b)
}
