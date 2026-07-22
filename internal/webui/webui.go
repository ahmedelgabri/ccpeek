// Package webui embeds and serves the SPA (built from ui/ by
// `just ui`). The build output ships inside the binary via go:embed, so
// distribution stays a single binary (docs/v2-plan.md §4.1). Since the
// v2.0 cutover the SPA mounts at /; /v2/ bookmarks redirect there.
//
// Two build variants exist, chosen by the `withui` build tag (see
// embed_withui.go / embed_apionly.go): the full product embeds the SPA
// and enforces its presence at COMPILE time; a plain `go build` /
// `go install` produces the explicitly API-only variant.
package webui

import (
	"io/fs"
	"net/http"
	"strings"
)

// Embedded reports whether this binary carries the built SPA — true
// only for the withui variant, whose compile-time embed guarantees the
// files exist.
func Embedded() bool {
	if !hasUI {
		return false
	}
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return false
	}
	_, err = fs.Stat(sub, "index.html")
	return err == nil
}

// Handler serves the SPA under the given URL prefix with history-routing
// fallback: paths that aren't embedded assets (client routes like
// /sessions/…) serve index.html and let the router take over. The
// API-only variant serves an explanation instead of a blank page.
func Handler(prefix string) http.Handler {
	if !Embedded() {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(missingUINotice))
		})
	}
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("webui: dist not embedded: " + err.Error())
	}
	return handlerFrom(sub, prefix)
}

func handlerFrom(sub fs.FS, prefix string) http.Handler {
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotImplemented)
			_, _ = w.Write([]byte(missingUINotice))
		})
	}
	fileServer := http.FileServer(http.FS(sub))

	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path != "" {
			if _, err := fs.Stat(sub, path); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})

	return http.StripPrefix(strings.TrimSuffix(prefix, "/"), fallback)
}

const missingUINotice = `This is the API-only ccpeek build variant (no embedded web UI).

The API under /api/v1 works normally; only the browser UI is missing.
Plain "go build" and "go install" produce this variant because they
cannot run the SPA build; the full product is compiled with the
"withui" build tag after building the UI.

To get the full UI, either:
  - install a released binary (Homebrew, Nix, or a GitHub release), or
  - build from a checkout with the UI included:  just build
`
