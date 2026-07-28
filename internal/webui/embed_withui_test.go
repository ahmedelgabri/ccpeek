//go:build withui

package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// These tests compile ONLY in the withui variant — the one every release
// path ships. They exercise the real embedded filesystem rather than a
// synthetic one, which nothing did before: the untagged test run never
// compiles embed_withui.go at all, so a mistake behind the build tag
// reached the release job unchecked.

// The compile-time embed guarantee has to hold at runtime too.
func TestEmbeddedReportsTheRealSPA(t *testing.T) {
	if !Embedded() {
		t.Fatal("Embedded() is false in the withui variant; the embed pattern should have failed the build")
	}
}

// The handler serves the actual built index and its actual assets.
func TestRealHandlerServesTheBuiltSPA(t *testing.T) {
	h := Handler("/")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<div id=\"root\"") {
		t.Errorf("index.html has no mount point: %.200s", body)
	}
	// The built index references hashed asset bundles; the handler must
	// serve whatever it points at.
	if !strings.Contains(body, "/assets/") {
		t.Errorf("index.html references no assets: %.200s", body)
	}
	if strings.Contains(body, "API-only") {
		t.Error("the withui build served the API-only notice")
	}
}

// Client routes fall back to index.html so deep links work on reload,
// and unknown asset paths do the same rather than 404ing into a blank
// page.
func TestRealHandlerFallsBackForClientRoutes(t *testing.T) {
	h := Handler("/")
	for _, path := range []string{
		"/sessions",
		"/sessions/claude-code/11111111-aaaa-bbbb-cccc-111111111111",
		"/artifacts/claude-code/memory/-home-u-x%2FMEMORY.md",
		"/usage",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Errorf("GET %s = %d, want 200 (index fallback)", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<div id=\"root\"") {
			t.Errorf("GET %s did not serve the SPA shell", path)
		}
	}
}

// The favicon is a real embedded file, not a fallback to index.html —
// proof that assets resolve against the embed rather than everything
// collapsing into the history-routing branch.
func TestRealHandlerServesEmbeddedAssets(t *testing.T) {
	h := Handler("/")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/favicon.svg", nil))
	if rec.Code != 200 {
		t.Fatalf("GET /favicon.svg = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<div id=\"root\"") {
		t.Error("favicon.svg fell back to index.html; assets are not resolving")
	}
	if !strings.Contains(rec.Body.String(), "<svg") {
		t.Errorf("favicon is not an SVG: %.100s", rec.Body.String())
	}
}
