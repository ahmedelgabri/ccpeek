package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/ahmedelgabri/ccexplore/internal/index"
	"github.com/ahmedelgabri/ccexplore/internal/model"
	"github.com/ahmedelgabri/ccexplore/internal/web"
)

// DataStore holds the loaded index data and the path to detail files.
type DataStore struct {
	Index   model.IndexData
	DataDir string
}

// ListenAndServe starts the HTTP server.
func ListenAndServe(addr, dataDir, claudeDir string, watch bool) error {
	store, err := loadDataStore(dataDir)
	if err != nil {
		return fmt.Errorf("loading data: %w", err)
	}

	tmpl, err := loadTemplates(web.FS)
	if err != nil {
		return fmt.Errorf("loading templates: %w", err)
	}

	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return fmt.Errorf("static fs: %w", err)
	}

	bookmarksFile := filepath.Join(filepath.Dir(dataDir), "ccexplore-bookmarks.json")
	h := &handlers{tmpl: tmpl, bookmarks: newBookmarkStore(bookmarksFile)}
	h.store.Store(store)

	if watch {
		go watchAndReindex(claudeDir, dataDir, &h.store)
	}

	return http.ListenAndServe(addr, requestLogger(registerRoutes(h, staticFS)))
}

// NewHandler creates the HTTP handler without starting a listener.
// Used for testing.
func NewHandler(dataDir string) (http.Handler, error) {
	store, err := loadDataStore(dataDir)
	if err != nil {
		return nil, fmt.Errorf("loading data: %w", err)
	}

	tmpl, err := loadTemplates(web.FS)
	if err != nil {
		return nil, fmt.Errorf("loading templates: %w", err)
	}

	staticFS, err := fs.Sub(web.FS, "static")
	if err != nil {
		return nil, fmt.Errorf("static fs: %w", err)
	}

	bookmarksFile := filepath.Join(filepath.Dir(dataDir), filepath.Base(dataDir)+"-bookmarks.json")
	h := &handlers{tmpl: tmpl, bookmarks: newBookmarkStore(bookmarksFile)}
	h.store.Store(store)

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
	mux.HandleFunc("GET /projects/{dirName}/{sessionId}/export.md", h.conversationExport)
	mux.HandleFunc("GET /search/{$}", h.search)
	mux.HandleFunc("GET /file-history/{$}", h.fileHistoryList)
	mux.HandleFunc("GET /file-history/{conversationId}/{$}", h.fileHistoryDetail)
	mux.HandleFunc("POST /bookmarks/toggle", h.bookmarkToggle)
	return mux
}

func loadDataStore(dataDir string) (*DataStore, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("reading index.json: %w", err)
	}

	var idx model.IndexData
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing index.json: %w", err)
	}

	return &DataStore{Index: idx, DataDir: dataDir}, nil
}

type handlers struct {
	store     atomic.Pointer[DataStore]
	tmpl      *templates
	bookmarks *BookmarkStore
}

const watchInterval = 30 * time.Second

func watchAndReindex(claudeDir, dataDir string, store *atomic.Pointer[DataStore]) {
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	for range ticker.C {
		log.Println("Re-indexing...")
		if err := index.Run(claudeDir, dataDir); err != nil {
			log.Printf("Re-index failed: %v", err)
			continue
		}
		newStore, err := loadDataStore(dataDir)
		if err != nil {
			log.Printf("Failed to load re-indexed data: %v", err)
			continue
		}
		store.Store(newStore)
		log.Println("Re-index complete, data reloaded.")
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
