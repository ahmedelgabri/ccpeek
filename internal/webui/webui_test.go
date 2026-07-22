package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// TestMissingUIServesExplanation: the API-only variant (plain go build,
// no withui tag) must say so instead of serving a blank page.
func TestMissingUIServesExplanation(t *testing.T) {
	h := handlerFrom(fstest.MapFS{
		".gitkeep": &fstest.MapFile{},
	}, "/")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "API-only ccpeek build variant") {
		t.Errorf("body does not explain the missing UI: %q", rec.Body.String())
	}
}

// TestBuiltUIServesIndexWithFallback: with a built SPA, assets serve
// directly and client routes fall back to index.html.
func TestBuiltUIServesIndexWithFallback(t *testing.T) {
	h := handlerFrom(fstest.MapFS{
		"index.html":  &fstest.MapFile{Data: []byte("<html>app</html>")},
		"assets/x.js": &fstest.MapFile{Data: []byte("js")},
	}, "/")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/sessions/claude-code/abc", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "app") {
		t.Errorf("client route = %d %q, want index fallback", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/assets/x.js", nil))
	if rec.Code != 200 || rec.Body.String() != "js" {
		t.Errorf("asset = %d %q", rec.Code, rec.Body.String())
	}
}
