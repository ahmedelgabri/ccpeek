package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccexplore/internal/index"
)

func setupTestServer(t *testing.T) http.Handler {
	t.Helper()

	testdataDir := filepath.Join("..", "..", "testdata")
	dataDir := t.TempDir()

	if err := index.Run(testdataDir, dataDir); err != nil {
		t.Fatal("index failed:", err)
	}

	handler, err := NewHandler(dataDir)
	if err != nil {
		t.Fatal("NewHandler failed:", err)
	}

	return handler
}

func TestDashboard(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	assertions := []string{"Dashboard", "Projects", "Plans", "Shell Snapshots", "Todos", "File History", "Recent Conversations"}
	for _, s := range assertions {
		if !strings.Contains(body, s) {
			t.Errorf("dashboard missing %q", s)
		}
	}
	// All history entries should be clickable links via encodeProjectDir
	if !strings.Contains(body, `href="/projects/test-project/"`) {
		t.Error("dashboard missing clickable project link in history")
	}
	if !strings.Contains(body, `href="/projects/-Users-test-project2/"`) {
		t.Error("dashboard missing encoded project link for /Users/test/project2")
	}
}

func TestPlansList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/plans/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Test Plan Title") {
		t.Error("plans list missing test plan title")
	}
	if !strings.Contains(body, "Filter plans...") {
		t.Error("plans list missing search input")
	}
}

func TestPlanDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/plans/test-plan/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Test Plan Title") {
		t.Error("plan detail missing title")
	}
	// Markdown should render
	if !strings.Contains(body, "Step one") {
		t.Error("plan detail missing rendered markdown content")
	}
}

func TestPlanNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/plans/nonexistent/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSnapshotsList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/shell-snapshots/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "snapshot-zsh-1700000000-abc123.sh") {
		t.Error("snapshots list missing test snapshot")
	}
}

func TestSnapshotDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/shell-snapshots/snapshot-zsh-1700000000-abc123/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Chroma should render syntax-highlighted code
	if !strings.Contains(body, "test snapshot") {
		t.Error("snapshot detail missing content")
	}
}

func TestTodosList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/todos/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-") {
		t.Error("todos list missing todo filename")
	}
	if !strings.Contains(body, "3 items") {
		t.Error("todos list missing item count")
	}
	if !strings.Contains(body, "test/project") {
		t.Error("todos list missing project name context")
	}
}

func TestTodoDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/todos/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-11111111-2222-3333-4444-555555555555/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Fix the bug") {
		t.Error("todo detail missing item content")
	}
	if !strings.Contains(body, "completed") {
		t.Error("todo detail missing status")
	}
	// Session back-link
	if !strings.Contains(body, "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/") {
		t.Error("todo detail missing session back-link")
	}
}

func TestProjectsList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "test-project") || !strings.Contains(body, "Filter projects...") {
		t.Error("projects list missing expected content")
	}
}

func TestSessionsList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "session") {
		t.Error("sessions list missing session info")
	}
	// Badges for linked entities
	if !strings.Contains(body, ">todo</span") {
		t.Error("sessions list missing todo badge")
	}
	if !strings.Contains(body, ">files</span") {
		t.Error("sessions list missing files badge")
	}
}

func TestConversation(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "hello world") {
		t.Error("conversation missing first prompt")
	}
	if !strings.Contains(body, "3 messages") {
		t.Error("conversation missing message count")
	}
	if !strings.Contains(body, "message-user") {
		t.Error("conversation missing user messages")
	}
	if !strings.Contains(body, "message-assistant") {
		t.Error("conversation missing assistant messages")
	}
	// Tab bar with links to todos and file history
	if !strings.Contains(body, "/todos/") {
		t.Error("conversation missing todos tab link")
	}
	if !strings.Contains(body, "/file-history/") {
		t.Error("conversation missing file history tab link")
	}
}

func TestConversationTodos(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/todos/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Fix the bug") {
		t.Error("conversation todos missing item content")
	}
	if !strings.Contains(body, "completed") {
		t.Error("conversation todos missing status")
	}
	// Tab bar should show active todos tab
	if !strings.Contains(body, "Todos") {
		t.Error("conversation todos missing tabs")
	}
}

func TestConversationTodosNotFound(t *testing.T) {
	handler := setupTestServer(t)
	// Session that doesn't have a todo file
	req := httptest.NewRequest("GET", "/projects/test-project/nonexistent/todos/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestConversationFileHistory(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/file-history/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "abc123") {
		t.Error("conversation file history missing hash")
	}
	if !strings.Contains(body, "2 file versions") {
		t.Error("conversation file history missing version count")
	}
	// Tab bar should show active file history tab
	if !strings.Contains(body, "File History") {
		t.Error("conversation file history missing tabs")
	}
}

func TestConversationFileHistoryNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/nonexistent/file-history/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestFileHistoryList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/file-history/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") {
		t.Error("file history list missing conversation ID")
	}
	if !strings.Contains(body, "test/project") {
		t.Error("file history list missing project name context")
	}
}

func TestFileHistoryDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/file-history/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "abc123") {
		t.Error("file history detail missing hash")
	}
	if !strings.Contains(body, "2 file versions") {
		t.Error("file history detail missing version count")
	}
	// Session back-link
	if !strings.Contains(body, "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/") {
		t.Error("file history detail missing session back-link")
	}
}

func TestStaticFiles(t *testing.T) {
	handler := setupTestServer(t)

	for _, path := range []string{"/static/style.css", "/static/app.js"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("GET %s: expected 200, got %d", path, w.Code)
		}
		if w.Body.Len() == 0 {
			t.Errorf("GET %s: empty body", path)
		}
	}
}
