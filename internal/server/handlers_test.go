package server

import (
	"context"
	"fmt"
	"io"
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

	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal("opening store:", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := index.Run(context.Background(), testdataDir, db, true, io.Discard); err != nil {
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
	assertions := []string{"Dashboard", "Projects", "Plans", "Shell Snapshots", "Commands", "Todos", "File History", "Tasks", "Paste Cache", "Usage Data", "Memories", "Secret Scan", "Recent Conversations"}
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
	if !strings.Contains(body, "test-project") {
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
	if !strings.Contains(body, `id="item-0"`) {
		t.Error("todo detail missing id attributes for deep linking")
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
	// Messages should have id attributes for deep linking
	if !strings.Contains(body, `id="msg-`) {
		t.Error("conversation messages missing id attributes for deep linking")
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
	if !strings.Contains(body, `id="cmd-`) {
		t.Error("commands page missing id attributes for deep linking")
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
	if !strings.Contains(body, `id="tool-`) {
		t.Error("tools page missing id attributes for deep linking")
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
	if !strings.Contains(body, `id="code-`) {
		t.Error("code page missing id attributes for deep linking")
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

	// Empty search page
	req := httptest.NewRequest("GET", "/search/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Search") {
		t.Error("search page missing title")
	}

	// Search for conversation content
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
	if !strings.Contains(body, "Conversations") {
		t.Error("search results missing Conversations group header")
	}
	// Conversation search results should deep-link with fragment anchors
	if !strings.Contains(body, "#msg-") {
		t.Error("search results missing #msg- fragment anchors for deep linking")
	}

	// No results
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

func TestSearchMemories(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=Architecture", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Memories") {
		t.Error("search for Architecture missing Memories group")
	}
	if !strings.Contains(body, "/memories/") {
		t.Error("search for Architecture missing memory link")
	}
}

func TestSearchPlans(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=Step+one", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Plans") {
		t.Error("search for Step one missing Plans group")
	}
	if !strings.Contains(body, "/plans/") {
		t.Error("search for Step one missing plan link")
	}
}

func TestSearchCommands(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=ls", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Commands") {
		t.Error("search for ls missing Commands group")
	}
	if !strings.Contains(body, "#cmd-") {
		t.Error("search for ls missing #cmd- fragment anchors for deep linking")
	}
}

func TestSearchPasteCache(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=clipboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Paste Cache") {
		t.Error("search for clipboard missing Paste Cache group")
	}
	if !strings.Contains(body, "/paste-cache/") {
		t.Error("search for clipboard missing paste cache link")
	}
}

func TestSearchTodos(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=Fix+the+bug", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Todos") {
		t.Error("search for Fix the bug missing Todos group")
	}
	if !strings.Contains(body, "/todos/") {
		t.Error("search for Fix the bug missing todo link")
	}
	if !strings.Contains(body, "#item-") {
		t.Error("search for Fix the bug missing #item- fragment anchors for deep linking")
	}
}

func TestSearchTasks(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=project+structure", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Tasks") {
		t.Error("search for project structure missing Tasks group")
	}
	if !strings.Contains(body, "/tasks/") {
		t.Error("search for project structure missing task link")
	}
	if !strings.Contains(body, "#task-") {
		t.Error("search for project structure missing #task- fragment anchors for deep linking")
	}
}

func TestSearchUsageData(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/search/?q=web+dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Usage Data") {
		t.Error("search for web dashboard missing Usage Data group")
	}
	if !strings.Contains(body, "/usage-data/") {
		t.Error("search for web dashboard missing usage data link")
	}
}

func TestSearchGroupedResults(t *testing.T) {
	handler := setupTestServer(t)
	// "test" should match across multiple types
	req := httptest.NewRequest("GET", "/search/?q=test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should have results from multiple groups
	groupCount := 0
	for _, group := range []string{"Conversations", "Plans", "Memories"} {
		if strings.Contains(body, group) {
			groupCount++
		}
	}
	if groupCount < 2 {
		t.Errorf("search for 'test' should match at least 2 groups, got %d", groupCount)
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
	if !strings.Contains(body, "test-project") {
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
	if !strings.Contains(body, `id="task-`) {
		t.Error("task detail missing id attributes for deep linking")
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

func TestCommandsList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Commands") {
		t.Error("commands list missing title")
	}
	if !strings.Contains(body, "ls -la") {
		t.Error("commands list missing bash command from testdata")
	}
	if !strings.Contains(body, "data-copy") {
		t.Error("commands list missing copy buttons")
	}
	if !strings.Contains(body, "Export") {
		t.Error("commands list missing export button")
	}
	if !strings.Contains(body, "format=zsh") {
		t.Error("commands list missing zsh export link")
	}
	if !strings.Contains(body, "format=bash") {
		t.Error("commands list missing bash export link")
	}
	if !strings.Contains(body, "format=fish") {
		t.Error("commands list missing fish export link")
	}
}

func TestCommandsListFilter(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/?search=ls", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "ls -la") {
		t.Error("filtered commands missing matching command")
	}
}

func TestCommandsListNoResults(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/?search=nonexistentcommand", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if strings.Contains(body, "Append to your shell history") {
		t.Error("commands with no results should not show export dropdown")
	}
}

func TestCommandsExportPlain(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/export?format=plain", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Errorf("expected text/plain, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "ls -la") {
		t.Error("plain export missing command")
	}
	// Plain format should not have zsh timestamps
	if strings.Contains(body, ":0;") {
		t.Error("plain export should not contain zsh format")
	}
}

func TestCommandsExportZsh(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/export?format=zsh", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, ":0;ls -la") {
		t.Error("zsh export missing command in zsh format")
	}
}

func TestCommandsExportBash(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/export?format=bash", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "ls -la") {
		t.Error("bash export missing command")
	}
}

func TestCommandsExportFish(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/export?format=fish", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "- cmd: ls -la") {
		t.Error("fish export missing command in fish format")
	}
	if !strings.Contains(body, "when:") {
		t.Error("fish export missing timestamp")
	}
}

func TestCommandsExportInvalidFormat(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/export?format=wat", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "unsupported format") {
		t.Error("invalid export should explain unsupported format")
	}
}

func setupTestServerWithFindings(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()

	testdataDir := filepath.Join("..", "..", "testdata")

	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal("opening store:", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := index.Run(context.Background(), testdataDir, db, true, io.Discard); err != nil {
		t.Fatal("index failed:", err)
	}

	// Insert scan findings with different rules and source types
	tx, err := db.BeginTx(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []model.ScanFinding{
		{RuleID: "aws-access-token", Description: "AWS key", SourceType: "message", SourceID: "test-session-abc", MatchRedacted: "AKIA****MZXB", ScannedAt: "2025-01-01T00:00:00Z"},
		{RuleID: "aws-access-token", Description: "AWS key", SourceType: "command", SourceID: "42", MatchRedacted: "AKIA****MZXB", ScannedAt: "2025-01-01T00:00:00Z"},
		{RuleID: "generic-api-key", Description: "Generic API Key", SourceType: "plan", SourceID: "test-plan.md", MatchRedacted: "s3cr****l0ng", ScannedAt: "2025-01-01T00:00:00Z"},
	} {
		if _, err := db.InsertScanFinding(context.Background(), tx, f); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	handler, err := NewHandler(db)
	if err != nil {
		t.Fatal("NewHandler failed:", err)
	}

	return handler, db
}

func TestScanList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/scan/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Secret Scan") {
		t.Error("scan page missing title")
	}
	if !strings.Contains(body, "gitleaks") {
		t.Error("scan page missing gitleaks attribution")
	}
}

func TestScanListWithFindings(t *testing.T) {
	handler, _ := setupTestServerWithFindings(t)
	req := httptest.NewRequest("GET", "/scan/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should show finding count and rule names
	for _, s := range []string{"3 potential secret", "aws-access-token", "generic-api-key"} {
		if !strings.Contains(body, s) {
			t.Errorf("scan list missing %q", s)
		}
	}
	// Should show redacted matches
	if !strings.Contains(body, "AKIA****MZXB") {
		t.Error("scan list missing redacted match")
	}
	// Should show source type labels
	if !strings.Contains(body, "message") || !strings.Contains(body, "command") || !strings.Contains(body, "plan") {
		t.Error("scan list missing source type labels")
	}
	// Should have ignore buttons
	if !strings.Contains(body, "toggle-ignore") {
		t.Error("scan list missing ignore toggle forms")
	}
	// Should have source links
	if !strings.Contains(body, "/plans/test-plan/") {
		t.Error("scan list missing source link for plan finding")
	}
	// Should have id attributes for deep linking
	if !strings.Contains(body, `id="finding-`) {
		t.Error("scan list missing id attributes for deep linking")
	}
}

func TestScanListFilterByRule(t *testing.T) {
	handler, _ := setupTestServerWithFindings(t)
	req := httptest.NewRequest("GET", "/scan/?rule=generic-api-key", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	// Should only show the generic-api-key finding's redacted match
	if !strings.Contains(body, "s3cr****l0ng") {
		t.Error("filtered scan list missing generic-api-key finding")
	}
	// Should NOT show the aws finding's redacted match in the findings list
	// (it still appears in the summary cards)
	count := strings.Count(body, "AKIA****MZXB")
	// The match appears in the summary card but NOT as a finding card
	if strings.Contains(body, `text-red-300/80 bg-red-500/5`) && count > 2 {
		t.Error("filtered scan list should not show aws finding in results")
	}
}

func TestScanListFilterByType(t *testing.T) {
	handler, _ := setupTestServerWithFindings(t)
	req := httptest.NewRequest("GET", "/scan/?type=plan", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "s3cr****l0ng") {
		t.Error("filtered scan list missing plan finding")
	}
}

func TestScanListCacheHeaders(t *testing.T) {
	handler, _ := setupTestServerWithFindings(t)
	req := httptest.NewRequest("GET", "/scan/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	cc := w.Header().Get("Cache-Control")
	if !strings.Contains(cc, "no-cache") {
		t.Errorf("expected no-cache header, got %q", cc)
	}
}

func TestScanToggleIgnore(t *testing.T) {
	handler, db := setupTestServerWithFindings(t)

	// Get finding IDs
	findings, err := db.ListScanFindings(context.Background(), "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	id := findings[0].ID

	// POST to toggle ignore
	req := httptest.NewRequest("POST", fmt.Sprintf("/scan/%d/toggle-ignore", id), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", w.Code)
	}

	// Verify finding is now ignored
	findings, _ = db.ListScanFindings(context.Background(), "", "", true)
	for _, f := range findings {
		if f.ID == id && !f.Ignored {
			t.Error("finding should be ignored after toggle")
		}
	}

	// Non-ignored list should have one fewer
	active, _ := db.ListScanFindings(context.Background(), "", "", false)
	if len(active) != 2 {
		t.Errorf("expected 2 active findings after ignoring 1 of 3, got %d", len(active))
	}
}

func TestScanToggleIgnoreInvalidID(t *testing.T) {
	handler, _ := setupTestServerWithFindings(t)

	req := httptest.NewRequest("POST", "/scan/notanumber/toggle-ignore", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid id, got %d", w.Code)
	}
}

func TestScanListShowIgnored(t *testing.T) {
	handler, db := setupTestServerWithFindings(t)

	// Ignore one finding
	findings, _ := db.ListScanFindings(context.Background(), "", "", true)
	db.ToggleScanFindingIgnored(context.Background(), findings[0].ID)

	// Default view should hide the ignored finding
	req := httptest.NewRequest("GET", "/scan/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "2 potential secret") {
		t.Error("stats should show 2 after ignoring 1")
	}

	// show_ignored=1 should show all 3
	req = httptest.NewRequest("GET", "/scan/?show_ignored=1", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	body = w.Body.String()
	// All three findings should be in the rendered output
	if strings.Count(body, "toggle-ignore") != 3 {
		t.Errorf("expected 3 toggle-ignore forms with show_ignored, got %d", strings.Count(body, "toggle-ignore"))
	}
	// The ignored finding should have the "unignore" button
	if !strings.Contains(body, "Unignore") {
		t.Error("ignored finding should show Unignore button")
	}
}

func TestScanDashboardCountExcludesIgnored(t *testing.T) {
	handler, db := setupTestServerWithFindings(t)

	// Dashboard should show 3 findings
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "Secret Scan") {
		t.Error("dashboard missing Secret Scan card")
	}

	// Ignore all 3 findings
	findings, _ := db.ListScanFindings(context.Background(), "", "", true)
	for _, f := range findings {
		db.ToggleScanFindingIgnored(context.Background(), f.ID)
	}

	// Dashboard should now show 0
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	body = w.Body.String()
	// The scan card count is rendered as {{ len .Index.ScanFindings }}
	// With 0 findings, the slice is empty so it renders "0"
	if !strings.Contains(body, `Secret Scan`) {
		t.Error("dashboard missing Secret Scan card after ignoring all")
	}
}

func TestSearchTextFragments(t *testing.T) {
	handler := setupTestServer(t)

	req := httptest.NewRequest("GET", "/search/?q=hello", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, ":~:text=hello") {
		t.Error("search results missing Text Fragment directive (:~:text=hello)")
	}
	// Text fragment should be combined with element anchor
	if !strings.Contains(body, "#msg-") || !strings.Contains(body, ":~:text=") {
		t.Error("search results should combine element anchor with text fragment")
	}
}

func TestSearchTextFragmentsMultiWord(t *testing.T) {
	handler := setupTestServer(t)

	req := httptest.NewRequest("GET", "/search/?q=Fix+the+bug", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	// URL-encoded spaces: "Fix the bug" → "Fix+the+bug" or "Fix&#43;the&#43;bug" after HTML escaping
	if !strings.Contains(body, ":~:text=Fix") {
		t.Error("multi-word search missing text fragment directive")
	}
	if !strings.Contains(body, "Todos") {
		t.Error("multi-word search for 'Fix the bug' should match Todos")
	}
}

func TestClickableTimestampMessages(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	// Timestamps should be <a> tags linking to their own anchor
	if !strings.Contains(body, `href="#msg-`) {
		t.Error("message timestamps should be clickable self-anchor links")
	}
}

func TestClickableTimestampCommands(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/commands/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `href="#cmd-`) {
		t.Error("command timestamps should be clickable self-anchor links")
	}
}

func TestClickableTimestampTools(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/tools/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `href="#tool-`) {
		t.Error("tool call timestamps should be clickable self-anchor links")
	}
}

func TestClickableTimestampSessionsList(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/projects/test-project/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, `href="#s-`) {
		t.Error("session list timestamps should be clickable self-anchor links")
	}
}

func TestCommandsListDeepLinks(t *testing.T) {
	handler := setupTestServer(t)
	req := httptest.NewRequest("GET", "/commands/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	// Timestamp links should point to session commands tab with anchor
	if !strings.Contains(body, "/commands/#cmd-") {
		t.Error("commands list timestamps should deep-link to session commands tab")
	}
}

func TestUrlForInTemplates(t *testing.T) {
	handler := setupTestServer(t)

	// Conversation page back-link should use urlFor "session-anchor"
	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	body := w.Body.String()

	// Back-link should go to sessions list with session anchor
	if !strings.Contains(body, "/projects/test-project/#s-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee") {
		t.Error("conversation back-link should use session-anchor URL")
	}
	// Export link should use urlFor "session-export"
	if !strings.Contains(body, "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/export.md") {
		t.Error("conversation export link missing")
	}
}

func TestUrlForSessionTabs(t *testing.T) {
	handler := setupTestServer(t)

	req := httptest.NewRequest("GET", "/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/commands/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	body := w.Body.String()

	// Session tabs should use urlFor-generated URLs (no trailing double slashes, etc.)
	for _, tab := range []string{"todos", "commands", "tools", "code"} {
		expected := fmt.Sprintf("/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/%s/", tab)
		if !strings.Contains(body, expected) {
			t.Errorf("session tabs missing %s tab URL", tab)
		}
	}
}

func TestIdAttributesPresent(t *testing.T) {
	handler := setupTestServer(t)

	tests := []struct {
		path     string
		idPrefix string
		desc     string
	}{
		{"/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/", `id="msg-`, "conversation messages"},
		{"/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/commands/", `id="cmd-`, "conversation commands"},
		{"/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/tools/", `id="tool-`, "conversation tools"},
		{"/projects/test-project/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee/code/", `id="code-`, "conversation code"},
		{"/projects/test-project/", `id="s-`, "sessions list"},
		{"/todos/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-agent-11111111-2222-3333-4444-555555555555/", `id="item-`, "todo items"},
		{"/tasks/33333333-aaaa-bbbb-cccc-333333333333/", `id="task-`, "task items"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("GET %s: expected 200, got %d", tt.path, w.Code)
			continue
		}
		if !strings.Contains(w.Body.String(), tt.idPrefix) {
			t.Errorf("GET %s: %s missing %s id attributes", tt.path, tt.desc, tt.idPrefix)
		}
	}
}

func TestCSSTargetHighlight(t *testing.T) {
	handler := setupTestServer(t)

	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, ":target") {
		t.Error("CSS missing :target pseudo-class for deep-link highlighting")
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

func TestNegativeInputs(t *testing.T) {
	handler := setupTestServer(t)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		// Invalid page numbers
		{"negative page", "/projects/-Users-demo-code-api-server/?page=-1", 200},
		{"zero page", "/projects/-Users-demo-code-api-server/?page=0", 200},
		{"non-numeric page", "/projects/-Users-demo-code-api-server/?page=abc", 200},

		// Path traversal attempts
		{"path traversal in dirName", "/projects/..%2F..%2Fetc/", 404},
		{"path traversal in sessionId", "/projects/-Users-demo-code-api-server/..%2F..%2F/", 404},

		// Nonexistent resources
		{"nonexistent project", "/projects/nonexistent-project/", 404},
		{"nonexistent session", "/projects/-Users-demo-code-api-server/00000000-0000-0000-0000-000000000000/", 404},
		{"nonexistent plan", "/plans/nonexistent/", 404},
		{"nonexistent snapshot", "/shell-snapshots/nonexistent/", 404},

		// SQL injection-like strings in search
		{"sql injection in search", "/search/?q=%27+OR+1%3D1+--", 200},
		{"sql injection in command search", "/commands/?search=%27+UNION+SELECT+*+FROM+sessions+--", 200},

		// Very long query parameter
		{"long search query", "/search/?q=" + strings.Repeat("a", 10000), 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tt.wantStatus {
				t.Errorf("GET %s: expected %d, got %d", tt.path, tt.wantStatus, w.Code)
			}
		})
	}
}

func TestCSRFProtection(t *testing.T) {
	handler := setupTestServer(t)

	// POST from external origin should be rejected
	r := httptest.NewRequest("POST", "/scan/1/toggle-ignore", nil)
	r.Header.Set("Origin", "https://evil.com")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for external origin, got %d", w.Code)
	}

	// POST with no origin/referer should be allowed (same-origin browser behavior)
	r = httptest.NewRequest("POST", "/scan/999/toggle-ignore", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	// Expect either 404 (finding not found) or 303 (redirect), not 403
	if w.Code == http.StatusForbidden {
		t.Error("expected non-403 for request without origin header")
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := setupTestServer(t)

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want %q", got, "nosniff")
	}
	if got := w.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q, want %q", got, "SAMEORIGIN")
	}
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Error("Content-Security-Policy header is missing")
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP missing default-src 'self': %s", csp)
	}
	if strings.Contains(csp, "fonts.googleapis.com") || strings.Contains(csp, "fonts.gstatic.com") {
		t.Errorf("CSP should not allow external Google font origins: %s", csp)
	}
	if strings.Contains(w.Body.String(), "fonts.googleapis.com") || strings.Contains(w.Body.String(), "fonts.gstatic.com") {
		t.Error("layout should not reference external Google Fonts")
	}
}
