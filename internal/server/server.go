package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ahmedelgabri/claude-history/internal/model"
	"github.com/ahmedelgabri/claude-history/internal/web"
)

// DataStore holds the loaded index data and the path to detail files.
type DataStore struct {
	Index   model.IndexData
	DataDir string
}

// ListenAndServe starts the HTTP server.
func ListenAndServe(addr, dataDir string) error {
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

	mux := http.NewServeMux()
	h := &handlers{store: store, tmpl: tmpl}

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
	mux.HandleFunc("GET /file-history/{$}", h.fileHistoryList)
	mux.HandleFunc("GET /file-history/{conversationId}/{$}", h.fileHistoryDetail)

	return http.ListenAndServe(addr, mux)
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

	mux := http.NewServeMux()
	h := &handlers{store: store, tmpl: tmpl}

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
	mux.HandleFunc("GET /file-history/{$}", h.fileHistoryList)
	mux.HandleFunc("GET /file-history/{conversationId}/{$}", h.fileHistoryDetail)

	return mux, nil
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
	store *DataStore
	tmpl  *templates
}
