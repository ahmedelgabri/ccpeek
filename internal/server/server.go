package server

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	"github.com/ahmedelgabri/ccpeek/internal/web"
)

var logColors = os.Getenv("NO_COLOR") == "" && isTerminalFd(os.Stderr)

// ListenAndServe starts the HTTP server.
func ListenAndServe(ctx context.Context, addr string, db *store.Store, claudeDir string, watch bool, watchInterval time.Duration) error {
	tmpl, err := loadTemplates(web.FS)
	if err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}

	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}

	h := &handlers{tmpl: tmpl, store: db}

	if watch {
		go watchAndReindex(ctx, claudeDir, db, watchInterval)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           requestLogger(securityHeaders(registerRoutes(h, staticFS))),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	err = srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// NewHandler creates the HTTP handler without starting a listener.
// Used for testing.
func NewHandler(db *store.Store) (http.Handler, error) {
	tmpl, err := loadTemplates(web.FS)
	if err != nil {
		return nil, fmt.Errorf("loading templates: %w", err)
	}

	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return nil, fmt.Errorf("static fs: %w", err)
	}

	h := &handlers{tmpl: tmpl, store: db}

	return securityHeaders(registerRoutes(h, staticFS)), nil
}

func registerRoutes(h *handlers, staticFS fs.FS) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("GET /{$}", h.dashboard)
	mux.HandleFunc("GET /plans/{$}", h.plansList)
	mux.HandleFunc("GET /plans/{fileName}/{$}", h.planDetail)
	mux.HandleFunc("GET /shell-snapshots/{$}", h.snapshotsList)
	mux.HandleFunc("GET /shell-snapshots/{fileName}/{$}", h.snapshotDetail)
	mux.HandleFunc("GET /todos/{$}", h.todosList)
	mux.HandleFunc("GET /todos/{fileName}/{$}", h.todoDetail)
	mux.HandleFunc("GET /projects/{$}", h.projectsList)
	mux.HandleFunc("GET /projects/{dirName}/{$}", h.sessionsList)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/{$}", h.conversation)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/todos/{$}", h.conversationTodos)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/file-history/{$}", h.conversationFileHistory)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/commands/{$}", h.conversationCommands)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/tools/{$}", h.conversationTools)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/code/{$}", h.conversationCode)
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/export.md", h.conversationExport)
	mux.HandleFunc("GET /commands/{$}", h.commandsList)
	mux.HandleFunc("GET /commands/export", h.commandsExport)
	mux.HandleFunc("GET /search/{$}", h.search)
	mux.HandleFunc("GET /file-history/{$}", h.fileHistoryList)
	mux.HandleFunc("GET /file-history/{conversationId}/{$}", h.fileHistoryDetail)
	mux.HandleFunc("GET /projects/{dirName}/compare", h.sessionCompare)
	mux.HandleFunc("GET /tasks/{$}", h.tasksList)
	mux.HandleFunc("GET /tasks/{dirName}/{$}", h.taskDetail)
	mux.HandleFunc("GET /paste-cache/{$}", h.pasteCacheList)
	mux.HandleFunc("GET /paste-cache/{fileName}/{$}", h.pasteCacheDetail)
	mux.HandleFunc("GET /usage-data/{$}", h.usageDataList)
	mux.HandleFunc("GET /usage-data/report/{$}", h.usageDataReport)
	mux.HandleFunc("GET /usage-data/report/raw", h.usageReportRaw)
	mux.HandleFunc("GET /usage-data/{sessionId}/{$}", h.usageDataDetail)
	mux.HandleFunc("GET /memories/{$}", h.memoriesList)
	mux.HandleFunc("GET /memories/{projectDir}/{$}", h.memoryDetail)
	mux.HandleFunc("GET /scan/{$}", h.scanList)
	mux.HandleFunc("POST /scan/{id}/toggle-ignore", h.scanToggleIgnore)
	return mux
}

type handlers struct {
	store *store.Store
	tmpl  *templates
}

func watchAndReindex(ctx context.Context, claudeDir string, db *store.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			changed, err := index.RunIncremental(ctx, claudeDir, db)
			if err != nil {
				log.Printf("Re-index failed: %v", err)
				continue
			}
			if changed {
				log.Println("Re-index complete.")
			}
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func statusColorCode(code int) string {
	if !logColors {
		return ""
	}
	switch {
	case code >= 500:
		return "\033[31m" // red
	case code >= 400:
		return "\033[33m" // yellow
	case code >= 300:
		return "\033[36m" // cyan
	default:
		return "\033[32m" // green
	}
}

func isTerminalFd(f *os.File) bool {
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data:; font-src 'self'")
		next.ServeHTTP(w, r)
	})
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start).Round(time.Microsecond)
		if logColors {
			log.Printf("\033[35m%s\033[0m %s %s%d\033[0m \033[2m%s\033[0m",
				r.Method, r.URL.Path, statusColorCode(rec.status), rec.status, elapsed)
		} else {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, elapsed)
		}
	})
}
