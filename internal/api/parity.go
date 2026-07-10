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
	q := r.URL.Query()
	list, err := h.svc.Artifacts(r.Context(), query.ArtifactsFilter{
		Agent:  q.Get("agent"),
		Kind:   q.Get("kind"),
		Limit:  intParam(q.Get("limit")),
		Offset: intParam(q.Get("offset")),
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
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

func (h *handlers) scanFindings(w http.ResponseWriter, r *http.Request) {
	includeIgnored := r.URL.Query().Get("ignored") == "1"
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
	q := r.URL.Query()
	blocks, err := h.svc.Blocks(r.Context(), q.Get("agent"), intParam(q.Get("limit")))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, blocks)
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
