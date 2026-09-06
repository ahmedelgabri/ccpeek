package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMarkdownImagesRequireExplicitNavigation(t *testing.T) {
	for _, src := range []string{"https://example.invalid/pixel?secret=private", "//example.invalid/pixel", "data:image/svg+xml,foo", "javascript:alert(1)"} {
		got := renderMarkdown("![image](" + src + ")")
		if strings.Contains(got, "<img") || strings.Contains(got, " src=") {
			t.Fatalf("automatic image: %s", got)
		}
	}
	if got := renderMarkdown("![image](https://example.invalid/pixel)"); !strings.Contains(got, "Open external image") {
		t.Fatal(got)
	}
}

func TestStaticReportRemovesNavigationAndActiveElements(t *testing.T) {
	got := staticReport(`<meta http-equiv="refresh" content="0;url=https://example.invalid/leak"><script>fetch('https://example.invalid')</script><iframe src="https://example.invalid"></iframe><p>keep report</p>`)
	if strings.Contains(got, "example.invalid") || !strings.Contains(got, "keep report") {
		t.Fatal(got)
	}
}

func TestStaticReportDropsNoscriptRawMarkup(t *testing.T) {
	for _, content := range []string{
		`<html><head><noscript><meta http-equiv="refresh" content="0;url=https://example.invalid/leak"></noscript></head><body><p>keep report</p></body></html>`,
		`<body><noscript><iframe src="https://example.invalid/leak"></iframe></noscript><p>keep report</p></body>`,
	} {
		got := staticReport(content)
		if strings.Contains(got, "noscript") || strings.Contains(got, "example.invalid") || !strings.Contains(got, "keep report") {
			t.Fatal(got)
		}
		if again := staticReport(got); again != got {
			t.Fatalf("unstable serialization: %s", again)
		}
	}
}

func TestMutationOriginMatchesExactServerOrigin(t *testing.T) {
	for _, origin := range []string{"http://127.0.0.1:3000", "http://127.0.0.1:4000", "http://localhost:3000", "https://127.0.0.1:3000", "null", "http://127.0.0.1:3000/"} {
		req := httptest.NewRequest("POST", "http://127.0.0.1:3000/api", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		sameOriginOnly(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })(rec, req)
		want := 403
		if origin == "http://127.0.0.1:3000" {
			want = 204
		}
		if rec.Code != want {
			t.Errorf("%s: got %d want %d", origin, rec.Code, want)
		}
	}
}
