package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeak/internal/index"
	"github.com/ahmedelgabri/ccpeak/internal/model"
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
	// History entries should be clickable links via encodeProjectDir
	if !strings.Contains(body, `href="/projects/`) {
		t.Error("dashboard missing clickable project links in history")
	}
	// Recent history should contain demo projects
	if !strings.Contains(body, `href="/projects/-Users-demo-code-web-app/"`) &&
		!strings.Contains(body, `href="/projects/-Users-demo-code-api-server/"`) {
		t.Error("dashboard missing recent demo project links in history")
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
	if !strings.Contains(body, ">commands</span") {
		t.Error("sessions list missing commands badge")
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
	if !strings.Contains(body, "5 messages") {
		t.Error("conversation missing message count")
	}
	if !strings.Contains(body, "message-user") {
		t.Error("conversation missing user messages")
	}
	if !strings.Contains(body, "message-assistant") {
		t.Error("conversation missing assistant messages")
	}
	// Tab bar with links to todos, file history, and commands
	if !strings.Contains(body, "/todos/") {
		t.Error("conversation missing todos tab link")
	}
	if !strings.Contains(body, "/file-history/") {
		t.Error("conversation missing file history tab link")
	}
	if !strings.Contains(body, "/commands/") {
		t.Error("conversation missing commands tab link")
	}
	if !strings.Contains(body, "/tools/") {
		t.Error("conversation missing tools tab link")
	}
	if !strings.Contains(body, "/code/") {
		t.Error("conversation missing code tab link")
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

func TestConversationCommands(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/commands/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "ls -la") {
		t.Error("commands page missing bash command")
	}
	if !strings.Contains(body, "data-copy") {
		t.Error("commands page missing copy button")
	}
	if !strings.Contains(body, "1 commands") {
		t.Error("commands page missing command count")
	}
	// Tab bar should show active commands tab
	if !strings.Contains(body, "Commands") {
		t.Error("commands page missing tabs")
	}
}

func TestConversationTools(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/tools/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Bash") {
		t.Error("tools page missing Bash tool name")
	}
	if !strings.Contains(body, "ls -la") {
		t.Error("tools page missing tool detail")
	}
	if !strings.Contains(body, "Tools") {
		t.Error("tools page missing tab")
	}
}

func TestConversationCode(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/code/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Write") {
		t.Error("code page missing Write tool label")
	}
	if !strings.Contains(body, "/tmp/test.go") {
		t.Error("code page missing file path")
	}
	if !strings.Contains(body, "package main") {
		t.Error("code page missing file content")
	}
	if !strings.Contains(body, "data-copy") {
		t.Error("code page missing copy button")
	}
	if !strings.Contains(body, "1 code operations") {
		t.Error("code page missing block count")
	}
	if !strings.Contains(body, "Code") {
		t.Error("code page missing tab")
	}
}

func TestConversationCodeNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/nonexistent/code/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestConversationExport(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/export.md", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/markdown") {
		t.Errorf("expected text/markdown content type, got %q", ct)
	}

	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("expected attachment disposition, got %q", cd)
	}

	body := w.Body.String()
	if !strings.Contains(body, "hello world") {
		t.Error("export missing first prompt")
	}
	if !strings.Contains(body, "## User") {
		t.Error("export missing User heading")
	}
	if !strings.Contains(body, "## Assistant") {
		t.Error("export missing Assistant heading")
	}
}

func TestConversationCommandsNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/nonexistent/commands/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSearch(t *testing.T) {
	handler := setupTestServer(t)

	// Empty query shows search page
	req := httptest.NewRequest("GET", "/search/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Search") {
		t.Error("search page missing title")
	}

	// Query with results
	req = httptest.NewRequest("GET", "/search/?q=hello", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "hello") {
		t.Error("search results missing matching content")
	}
	if !strings.Contains(body, `for "hello"`) {
		t.Error("search results missing query echo")
	}

	// Query with no results
	req = httptest.NewRequest("GET", "/search/?q=zzzznonexistent", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `for "zzzznonexistent"`) {
		t.Error("search should echo non-matching query")
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

func TestSessionCompare(t *testing.T) {
	handler := setupTestServer(t)

	// Missing params returns 400
	req := httptest.NewRequest("GET", "/projects/test-project/compare", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400 for missing params, got %d", w.Code)
	}

	// Same session for both (valid, just a degenerate case)
	sessionID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	req = httptest.NewRequest("GET", "/projects/test-project/compare?a="+sessionID+"&b="+sessionID, nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Compare Sessions") {
		t.Error("compare page missing title")
	}
	if !strings.Contains(body, "Messages") {
		t.Error("compare page missing metadata")
	}
}

func TestSessionCompareNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/compare?a=nonexistent&b=nonexistent2", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSnapshotNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/shell-snapshots/nonexistent/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestTodoNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/todos/nonexistent/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestConversationToolsNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/nonexistent/tools/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestExtractSnippet(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog"

	// Match at the start
	snippet := extractSnippet(text, 0, 3, 10)
	if snippet == "" {
		t.Error("extractSnippet returned empty for match at start")
	}
	if strings.HasPrefix(snippet, "...") {
		t.Error("snippet at start should not have leading ellipsis")
	}
	if !strings.HasSuffix(snippet, "...") {
		t.Error("snippet at start should have trailing ellipsis")
	}

	// Match in the middle
	snippet = extractSnippet(text, 20, 5, 5)
	if !strings.HasPrefix(snippet, "...") {
		t.Error("snippet in middle should have leading ellipsis")
	}
	if !strings.HasSuffix(snippet, "...") {
		t.Error("snippet in middle should have trailing ellipsis")
	}

	// Match near the end
	snippet = extractSnippet(text, len(text)-3, 3, 10)
	if !strings.HasPrefix(snippet, "...") {
		t.Error("snippet at end should have leading ellipsis")
	}
	if strings.HasSuffix(snippet, "...") {
		t.Error("snippet at end should not have trailing ellipsis")
	}
}

func TestBuildHeatmap(t *testing.T) {
	// Empty history
	days := buildHeatmap(nil)
	if len(days) != 364 {
		t.Errorf("expected 364 days, got %d", len(days))
	}
	for _, d := range days {
		if d.Level != 0 {
			t.Errorf("empty history should have level 0, got %d for %s", d.Level, d.Date)
		}
	}

	// History with entries
	now := time.Now()
	today := now.UnixMilli()
	history := []model.HistoryEntry{
		{Timestamp: today, Display: "a", Project: "test"},
		{Timestamp: today, Display: "b", Project: "test"},
		{Timestamp: today, Display: "c", Project: "test"},
	}
	days = buildHeatmap(history)
	if len(days) != 364 {
		t.Errorf("expected 364 days, got %d", len(days))
	}
	// Last day should have a non-zero level
	lastDay := days[363]
	if lastDay.Count != 3 {
		t.Errorf("expected count 3 for today, got %d", lastDay.Count)
	}
	if lastDay.Level == 0 {
		t.Error("expected non-zero level for day with entries")
	}
}

func TestConversationPagination(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/?page=1", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Page 1 should contain message count info
	if !strings.Contains(body, "5 messages") {
		t.Error("pagination page missing message count")
	}
}

func TestSidebarSearchForm(t *testing.T) {
	handler := setupTestServer(t)

	pages := []string{
		"/",
		"/plans/",
		"/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/",
	}

	for _, path := range pages {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("GET %s: expected 200, got %d", path, w.Code)
			continue
		}

		body := w.Body.String()
		if !strings.Contains(body, "global-search-input") {
			t.Errorf("GET %s: missing global-search-input in sidebar", path)
		}
	}
}

func TestSearchPreservesQuery(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=hello", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// The sidebar search input should have the query value
	if !strings.Contains(body, `value="hello"`) {
		t.Error("search page sidebar input missing query value")
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
