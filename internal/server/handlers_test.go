package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/index"
	"github.com/ahmedelgabri/ccpeek/internal/model"
	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func setupTestServer(t *testing.T) http.Handler {
	t.Helper()

	testdataDir := filepath.Join("..", "..", "testdata")

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal("opening store:", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := index.Run(testdataDir, db); err != nil {
		t.Fatal("index failed:", err)
	}

	handler, err := NewHandler(db)
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
	assertions := []string{"Dashboard", "Projects", "Plans", "Shell Snapshots", "Todos", "File History", "Tasks", "Paste Cache", "Usage Data", "Memories", "Recent Conversations"}
	for _, s := range assertions {
		if !strings.Contains(body, s) {
			t.Errorf("dashboard missing %q", s)
		}
	}
	if !strings.Contains(body, `href="/projects/`) {
		t.Error("dashboard missing clickable project links in history")
	}
	if !strings.Contains(body, `href="/projects/-Users-demo-code-web-app/"`) &&
		!strings.Contains(body, `href="/projects/-Users-demo-code-api-server/"`) {
		t.Error("dashboard missing recent demo project links in history")
	}
	if !strings.Contains(body, "Tool Usage") {
		t.Error("dashboard missing tool usage section")
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

func TestSessionsListSort(t *testing.T) {
	handler := setupTestServer(t)

	for _, sort := range []string{"oldest", "messages", "tokens", "tools"} {
		req := httptest.NewRequest("GET", "/projects/test-project/?sort="+sort, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("sort=%s: expected 200, got %d", sort, w.Code)
		}
	}
}

func TestSessionsListBranchFilter(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/?branch=main", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "session") {
		t.Error("branch filter should return sessions")
	}
}

func TestSessionsListDateRange(t *testing.T) {
	handler := setupTestServer(t)

	req := httptest.NewRequest("GET", "/projects/test-project/?from=2024-01-01&to=2024-12-31", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, `value="2024-01-01"`) {
		t.Error("date range should preserve from value")
	}
	if !strings.Contains(body, `value="2024-12-31"`) {
		t.Error("date range should preserve to value")
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
	if !strings.Contains(body, "Todos") {
		t.Error("conversation todos missing tabs")
	}
}

func TestConversationTodosNotFound(t *testing.T) {
	handler := setupTestServer(t)
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

	req := httptest.NewRequest("GET", "/search/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Search") {
		t.Error("search page missing title")
	}

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
	if !strings.Contains(body, "<mark") {
		t.Error("search results missing highlight mark tags")
	}

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
	if !strings.Contains(body, "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/") {
		t.Error("file history detail missing session back-link")
	}
}

func TestSessionCompare(t *testing.T) {
	handler := setupTestServer(t)

	req := httptest.NewRequest("GET", "/projects/test-project/compare", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Errorf("expected 400 for missing params, got %d", w.Code)
	}

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
	for _, want := range []string{"Total tool calls", "Summary", "Diff"} {
		if !strings.Contains(body, want) {
			t.Errorf("compare page missing %q", want)
		}
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

	snippet = extractSnippet(text, 20, 5, 5)
	if !strings.HasPrefix(snippet, "...") {
		t.Error("snippet in middle should have leading ellipsis")
	}
	if !strings.HasSuffix(snippet, "...") {
		t.Error("snippet in middle should have trailing ellipsis")
	}

	snippet = extractSnippet(text, len(text)-3, 3, 10)
	if !strings.HasPrefix(snippet, "...") {
		t.Error("snippet at end should have leading ellipsis")
	}
	if strings.HasSuffix(snippet, "...") {
		t.Error("snippet at end should not have trailing ellipsis")
	}
}

func TestBuildHeatmap(t *testing.T) {
	days := buildHeatmap(nil)
	if len(days) != 364 {
		t.Errorf("expected 364 days, got %d", len(days))
	}
	for _, d := range days {
		if d.Level != 0 {
			t.Errorf("empty history should have level 0, got %d for %s", d.Level, d.Date)
		}
	}

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
	if !strings.Contains(body, `value="hello"`) {
		t.Error("search page sidebar input missing query value")
	}
}

func TestTasksList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/tasks/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "33333333-aaaa-bbbb-cccc-333333333333") {
		t.Error("tasks list missing task group UUID")
	}
	if !strings.Contains(body, "3 items") {
		t.Error("tasks list missing item count")
	}
}

func TestTaskDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/tasks/33333333-aaaa-bbbb-cccc-333333333333/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Set up project structure") {
		t.Error("task detail missing task subject")
	}
	if !strings.Contains(body, "completed") {
		t.Error("task detail missing completed status")
	}
	if !strings.Contains(body, "pending") {
		t.Error("task detail missing pending status")
	}
}

func TestTaskNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/tasks/nonexistent/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestPasteCacheList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/paste-cache/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "abcdef1234567890") {
		t.Error("paste cache list missing first entry")
	}
	if !strings.Contains(body, "fedcba0987654321") {
		t.Error("paste cache list missing second entry")
	}
}

func TestPasteCacheDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/paste-cache/abcdef1234567890/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "sample paste cache entry") {
		t.Error("paste cache detail missing content")
	}
}

func TestPasteCacheNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/paste-cache/nonexistent/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUsageDataList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/usage-data/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "web dashboard") {
		t.Error("usage data list missing first facet summary")
	}
	if !strings.Contains(body, "auth bug fix") {
		t.Error("usage data list missing second facet summary")
	}
	if !strings.Contains(body, "View Report") {
		t.Error("usage data list missing report link")
	}
}

func TestUsageDataDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/usage-data/33333333-aaaa-bbbb-cccc-333333333333/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Fully Achieved") {
		t.Error("usage data detail missing outcome")
	}
	if !strings.Contains(body, "Very Helpful") {
		t.Error("usage data detail missing helpfulness")
	}
	if !strings.Contains(body, "Feature Addition") {
		t.Error("usage data detail missing goal category")
	}
}

func TestUsageDataNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/usage-data/nonexistent/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestUsageDataReport(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/usage-data/report/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "iframe") {
		t.Error("usage report missing iframe element")
	}
}

func TestUsageReportRaw(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/usage-data/report/raw", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Usage Report") {
		t.Error("raw report missing content")
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("expected Content-Type text/html, got %q", ct)
	}
}

func TestMemoriesList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/memories/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "test-project") {
		t.Error("memories list missing project dir")
	}
	if !strings.Contains(body, "Filter memories...") {
		t.Error("memories list missing search input")
	}
}

func TestMemoryDetail(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/memories/test-project/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Architecture") {
		t.Error("memory detail missing rendered markdown content")
	}
	if !strings.Contains(body, "View Project") {
		t.Error("memory detail missing view project link")
	}
}

func TestMemoryNotFound(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/memories/nonexistent/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
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
