package cmd

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/api"
	"github.com/ahmedelgabri/ccpeek/internal/webui"
)

// buildServeHandler assembles the v2.0 serving layout: the SPA at /, the
// JSON API at /api/v1, and 301 redirects from every v1 UI route so
// bookmarks keep working (docs/v2-plan.md §8.2).
//
// api.LoopbackOnly wraps the whole layout, not just the API: binding
// 127.0.0.1 stops other machines from connecting, but it does not stop a
// web page whose hostname has been rebound to 127.0.0.1 from reading the
// archive through the victim's own browser. Checking the Host header is
// what distinguishes those.
func buildServeHandler(apiHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	spa := webui.Handler("/")
	mux.Handle("/api/", apiHandler)
	mux.Handle("/", spa)
	registerLegacyRedirects(mux, spa)
	return requestLog(api.LoopbackOnly(mux))
}

// registerLegacyRedirects maps v1 routes to their session-centric
// equivalents. Explicit patterns take precedence over the "/" SPA mount.
func registerLegacyRedirects(mux *http.ServeMux, spa http.Handler) {
	redirect := func(target string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}
	}

	// Conversations: /projects/{dir}/{sessionId}/... → the session page.
	// Every v1 project was Claude Code by definition.
	sessionRedirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r,
			"/sessions/claude-code/"+r.PathValue("sessionId"),
			http.StatusMovedPermanently)
	}
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/{$}", sessionRedirect)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/{tab}/{$}", sessionRedirect)
	mux.HandleFunc("GET /projects/{dirName}/{$}", redirect("/"))
	mux.HandleFunc("GET /projects/{$}", redirect("/"))

	// Sidecar browsers → the unified artifacts page.
	for _, p := range []string{
		"/plans/", "/shell-snapshots/", "/todos/", "/tasks/",
		"/paste-cache/", "/usage-data/", "/memories/", "/file-history/",
	} {
		mux.Handle("GET "+p, redirect("/artifacts"))
	}

	// The legacy browsers map onto their current pages; they just lose the slash.
	mux.HandleFunc("GET /commands/{$}", redirect("/commands"))
	mux.HandleFunc("GET /scan/{$}", redirect("/scan"))
	mux.HandleFunc("GET /search/{$}", func(w http.ResponseWriter, r *http.Request) {
		target := "/search"
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	// Registering /x/{$} makes ServeMux 301 the unslashed /x to /x/, which
	// the handlers above bounce straight back — an infinite loop. Explicit
	// unslashed registrations suppress that implicit redirect and serve the
	// SPA client routes directly.
	mux.Handle("GET /commands", spa)
	mux.Handle("GET /scan", spa)
	mux.Handle("GET /search", spa)

	// The /v2/ preview mount: send bookmarks to the same path at /.
	mux.HandleFunc("GET /v2/", func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Path[len("/v2"):]
		if target == "" {
			target = "/"
		}
		if q := r.URL.RawQuery; q != "" {
			target += "?" + q
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// statusRecorder captures the response status for the access log. Flush
// must pass through — /api/v1/events streams SSE and dies without it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// requestLog is the access log: colored status class + method + path +
// duration (colors follow NO_COLOR / non-TTY via the shared color vars).
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		statusColor := colorGreen
		switch {
		case rec.status >= 500:
			statusColor = colorRed
		case rec.status >= 400:
			statusColor = colorYellow
		case rec.status >= 300:
			statusColor = colorDim
		}
		log.Printf("%s%d%s %s%-4s%s %s %s%s%s",
			statusColor, rec.status, colorReset,
			colorBold, r.Method, colorReset,
			r.URL.Path,
			colorDim, time.Since(start).Round(time.Microsecond), colorReset)
	})
}

// serve runs the HTTP server with graceful shutdown, mirroring v1's
// timeouts.
func serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		// No WriteTimeout: /api/v1/events streams SSE for the lifetime of
		// the page; a fixed deadline would sever live updates.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
