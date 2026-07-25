package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("api payload"))
	})
}

// The Host guard has to sit in front of the assembled layout, not inside
// the API mux — the SPA and the legacy redirects are reachable from a
// rebound host too, and a redirect leaks the URL structure of the archive.
func TestServeHandlerRejectsNonLoopbackHost(t *testing.T) {
	h := buildServeHandler(stubAPI())

	paths := []string{
		"/api/v1/sessions",
		"/",
		"/sessions/claude-code/abc",
		"/projects/dir/11111111-aaaa-bbbb-cccc-111111111111/",
		"/v2/usage",
	}
	for _, p := range paths {
		req := httptest.NewRequest("GET", "http://evil.example"+p, nil)
		req.Host = "evil.example:3000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a rebound host = %d, want 403", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "api payload") {
			t.Errorf("%s reached the API from a rebound host", p)
		}
	}
}

// …and it must not get in the way of the real thing.
func TestServeHandlerAllowsLoopback(t *testing.T) {
	h := buildServeHandler(stubAPI())

	for _, host := range []string{"127.0.0.1:3000", "localhost:3000", "[::1]:3000"} {
		req := httptest.NewRequest("GET", "http://x/api/v1/sessions", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "api payload" {
			t.Errorf("Host %q = %d %q, want 200 api payload", host, rec.Code, rec.Body.String())
		}
	}

	// Legacy redirects still work from loopback.
	req := httptest.NewRequest("GET", "http://x/projects/dir/session-id/", nil)
	req.Host = "127.0.0.1:3000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("legacy redirect = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/sessions/claude-code/session-id" {
		t.Errorf("Location = %q", got)
	}
}
