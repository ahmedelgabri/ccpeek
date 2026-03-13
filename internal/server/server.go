package server

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/store"
	"github.com/ahmedelgabri/ccpeek/internal/web"
)

// ListenAndServe starts the HTTP server.
func ListenAndServe(addr string, db *store.Store, claudeDir string, watch bool) error {
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
		go watchAndReindex(claudeDir, db)
	}

	return http.ListenAndServe(addr, requestLogger(registerRoutes(h, staticFS)))
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

	return registerRoutes(h, staticFS), nil
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
	return mux
}

type handlers struct {
	store *store.Store
	tmpl  *templates
}

const watchInterval = 30 * time.Second

func watchAndReindex(claudeDir string, db *store.Store) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for range ticker.C {
		changed, err := index.RunIncremental(claudeDir, db)
		if err != nil {
			log.Printf("Re-index failed: %v", err)
			continue
		}
		if changed {
			log.Println("Re-index complete.")
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

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start).Round(time.Microsecond)
		log.Printf("\033[35m%s\033[0m %s %s%d\033[0m \033[2m%s\033[0m",
			r.Method, r.URL.Path, statusColorCode(rec.status), rec.status, elapsed)
	})
}
