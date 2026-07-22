// Package webui embeds and serves the SPA (built from ui/ by
// `just ui`). The build output ships inside the binary via go:embed, so
// distribution stays a single binary (docs/v2-plan.md §4.1). Since the
// v2.0 cutover the SPA mounts at /; /v2/ bookmarks redirect there.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Embedded reports whether a built SPA is inside this binary. A plain
// `go build`/`go install` without a prior `just ui` embeds only the
// tracked dist/.gitkeep — the embed pattern still succeeds, so without
// this check such a binary would silently serve an empty page.
func Embedded() bool {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return false
	}
	_, err = fs.Stat(sub, "index.html")
	return err == nil
}

// Handler serves the SPA under the given URL prefix with history-routing
// fallback: paths that aren't embedded assets (client routes like
// /sessions/…) serve index.html and let the router take over. A binary
// built without the SPA serves an explanation instead of a blank page.
func Handler(prefix string) http.Handler {
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

const missingUINotice = `This ccpeek binary was built without the embedded web UI.

The API under /api/v1 works normally; only the browser UI is missing.
It happens when the binary is compiled directly with "go build" or
"go install", which cannot run the SPA build.

To get the full UI, either:
  - install a released binary (Homebrew, Nix, or a GitHub release), or
  - build from a checkout with the UI included:  just build
`
