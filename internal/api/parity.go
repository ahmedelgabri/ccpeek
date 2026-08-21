package api

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/ahmedelgabri/ccpeek/internal/ops"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// sameOriginOnly rejects cross-origin browser requests to mutating
// endpoints (CSRF guard, matching v1's toggle-ignore protection). The
// server only binds 127.0.0.1; this blocks malicious websites driving the
// local API through a victim's browser.
//
// It is not sufficient on its own — see LoopbackOnly, which covers the
// reads this cannot.
func sameOriginOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !isLoopbackHost(u.Hostname()) {
				writeEnvelope(w, http.StatusForbidden, ops.Envelope{
					Error: "cross-origin requests are not allowed",
				})
				return
			}
		}
		next(w, r)
	}
}

// LoopbackOnly rejects any request whose Host header is not a loopback
// name, and belongs in front of the WHOLE server — SPA included.
//
// The Origin check above cannot stand alone. Under DNS rebinding a page
// on evil.example, once the name resolves to 127.0.0.1, reaches the
// server at http://evil.example:3000 — which the browser treats as SAME
// origin, so it sends no Origin header and applies no CORS check. Every
// read (sessions, transcripts, search, scan findings) would answer,
// because nothing else in the request distinguishes it from a legitimate
// one. The Host header does: the browser sends the name the page used,
// and only a real loopback URL carries a loopback Host.
func LoopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(hostname(r.Host)) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("ccpeek serves 127.0.0.1 only; refusing request for host " +
				strconv.Quote(r.Host) + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostname strips any port from a Host header value, handling the
// bracketed IPv6 form. An empty Host (HTTP/1.0, or a raw client) is not
// loopback by default — callers must be explicit about where they point.
func hostname(host string) string {
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return strings.Trim(host, "[]")
}

// isLoopbackHost reports whether a hostname addresses this machine.
// Named loopback aliases are limited to "localhost" itself: RFC 6761
// reserves the .localhost subtree for loopback, but resolvers disagree in
// practice and an attacker-chosen evil.localhost that resolves elsewhere
// would otherwise pass. Literal addresses are checked numerically, so
// every spelling of loopback (127.0.0.2, ::ffff:127.0.0.1) is covered
// without an allowlist of strings.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.IsLoopback() || (ip.Is4In6() && ip.Unmap().IsLoopback())
	}
	return false
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
	writeJSON(w, http.StatusOK, list)
}

// artifactKinds is the browser's kind facet, agent-filtered to match the
// list beside it.
func (h *handlers) artifactKinds(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	agent := p.Str("agent")
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	out, err := h.svc.ArtifactKinds(r.Context(), agent)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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

// scanRules is the rule-first reading of the scan: which rules fired, how
// many occurrences are still active, and in how many entities.
func (h *handlers) scanRules(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ScanRules(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handlers) scanFindings(w http.ResponseWriter, r *http.Request) {
	p := newParams(r)
	includeIgnored := p.Bool("ignored")
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	findings, err := h.svc.ScanFindings(r.Context(), includeIgnored)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, findings)
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
	if err := decodeBody(w, r, &body); err != nil {
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
	agent, limit, costMode := p.Str("agent"), p.Int("limit"), p.Str("cost_mode")
	if err := p.Err(); err != nil {
		writeBadRequest(w, err)
		return
	}
	blocks, err := h.svc.BlocksWithCostMode(r.Context(), agent, limit, costMode)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, blocks)
}
