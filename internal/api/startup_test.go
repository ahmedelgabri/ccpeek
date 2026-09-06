package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInitializingAPI(t *testing.T) {
	h := Handler(nil, Deps{})
	for _, route := range Routes() {
		if route.Pattern == "GET /api/v1/events" {
			continue
		} // Exercised by the serving startup test.
		method, path, _ := strings.Cut(route.Pattern, " ")
		path = strings.NewReplacer("{agent}", "pi", "{id}", "test", "{seq}", "0", "{kind}", "memory", "{name}", "test").Replace(path)
		t.Run(route.Pattern, func(t *testing.T) {
			r := httptest.NewRequest(method, path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want, text := http.StatusServiceUnavailable, `"error":"archive is initializing"`
			switch path {
			case "/api/v1/health":
				want, text = http.StatusOK, `"initializing":true`
				if strings.Contains(w.Body.String(), `"archive"`) {
					t.Error("reported coverage before opening the archive")
				}
			case "/api/v1/ready":
				text = `"status":"initializing"`
			}
			if w.Code != want || !strings.Contains(w.Body.String(), text) {
				t.Fatalf("got %d %s; want %d containing %s", w.Code, w.Body.String(), want, text)
			}
			if !strings.Contains(w.Body.String(), `"schema":"ccpeek/v1"`) {
				t.Errorf("missing API envelope: %s", w.Body.String())
			}
			if route.Kind == "write" {
				r.Header.Set("Origin", "https://evil.example")
				w = httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Errorf("cross-origin mutation during startup: %d", w.Code)
				}
			}
		})
	}
}
