// Package webui embeds and serves the v2 SPA (built from ui/ by
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

// Handler serves the SPA under the given URL prefix with history-routing
// fallback: paths that aren't embedded assets (client routes like
// /sessions/…) serve index.html and let the router take over.
func Handler(prefix string) http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("webui: dist not embedded: " + err.Error())
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
