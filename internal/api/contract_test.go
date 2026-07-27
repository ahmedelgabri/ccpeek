package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

// emptyHandler serves the API over a store with no data at all — the
// contract tests need the zero case, not the fixture corpus.
func emptyHandler(t *testing.T) http.Handler {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	return Handler(query.New(store, table), nil, nil, nil, nil, nil)
}

func rawGet(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

// seededHandler serves the API over a store the test keeps a handle on, so
// it can write its own corpus and watch what happens when the store breaks.
func seededHandler(t *testing.T) (http.Handler, *db.Store) {
	t.Helper()
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	return Handler(query.New(store, table), nil, nil, nil, nil, nil), store
}

// TestCommandExportPagesToCompletion: ?format= is a DOWNLOAD, not a page
// of a list view. It served exactly one page, and the commands ceiling is
// 1000 — so a larger corpus produced a shell-history file missing
// everything older with nothing in it saying so, while `ccpeek export
// commands` (same data, same formats) paged to the end.
//
// It also pins the ordering ACROSS pages: history files read oldest-first
// and the op answers newest-first, so reversing each page as it arrives
// orders commands correctly within a page and backwards between them.
func TestCommandExportPagesToCompletion(t *testing.T) {
	h, store := seededHandler(t)
	ctx := context.Background()

	// Past the commands ceiling, so a single page cannot hold the corpus.
	total := query.CommandsLimit.Max + 500
	w, err := store.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sessionID, err := w.UpsertSession(canon.Session{
		Agent: "claude-code", ExternalID: "export-session",
	}, "h-export")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		if err := w.InsertToolCall(sessionID, canon.ToolCall{
			Seq: i, Name: "Bash", Kind: canon.ToolShell,
			Command:   fmt.Sprintf("echo %d", i),
			StartedAt: base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	lines := func(body string) []string {
		body = strings.TrimSuffix(body, "\n")
		if body == "" {
			return nil
		}
		return strings.Split(body, "\n")
	}

	code, body := rawGet(t, h, "/api/v1/commands?format=plain")
	if code != 200 {
		t.Fatalf("export = %d (%s)", code, strings.TrimSpace(body))
	}
	got := lines(body)
	if len(got) != total {
		t.Fatalf("exported %d commands, want the whole corpus of %d", len(got), total)
	}
	// Oldest first, end to end: echo 0 … echo N-1.
	for i, line := range got {
		if want := fmt.Sprintf("echo %d", i); line != want {
			t.Fatalf("export line %d = %q, want %q (pages reversed individually?)", i, line, want)
		}
	}

	// The JSON list view is unchanged: one default page.
	code, env := get(t, h, "/api/v1/commands")
	if code != 200 {
		t.Fatalf("commands list = %d (%s)", code, env.Error)
	}
	if rows, _ := env.Data.([]any); len(rows) != 100 {
		t.Errorf("list page = %d rows, want the op default of 100", len(rows))
	}

	// An explicitly supplied limit still bounds the export — the newest 10,
	// written oldest-first like the rest of the file.
	code, body = rawGet(t, h, "/api/v1/commands?format=plain&limit=10")
	if code != 200 {
		t.Fatalf("bounded export = %d (%s)", code, strings.TrimSpace(body))
	}
	got = lines(body)
	if len(got) != 10 {
		t.Fatalf("bounded export = %d commands, want 10", len(got))
	}
	if first, last := fmt.Sprintf("echo %d", total-10), fmt.Sprintf("echo %d", total-1); got[0] != first || got[9] != last {
		t.Errorf("bounded export = %q … %q, want %q … %q", got[0], got[9], first, last)
	}

	// And an over-cap limit stays a refusal that names the ceiling, not a
	// file silently clipped to the ceiling.
	code, body = rawGet(t, h, "/api/v1/commands?format=plain&limit="+strconv.Itoa(query.CommandsLimit.Max+1))
	if code != http.StatusBadRequest {
		t.Errorf("over-cap export = %d, want 400 (%s)", code, strings.TrimSpace(body))
	}

	// Other filters still narrow the download.
	code, body = rawGet(t, h, "/api/v1/commands?format=plain&query=echo+7")
	if code != 200 {
		t.Fatalf("filtered export = %d (%s)", code, strings.TrimSpace(body))
	}
	for _, line := range lines(body) {
		if !strings.Contains(line, "echo 7") {
			t.Errorf("filtered export line %q does not match the filter", line)
		}
	}
}

// TestScanIgnoreMissAndFailureAreDifferentStatuses: the ignore setter
// mapped every failure of its lookup to ErrNotFound, so a broken store
// answered 404 "scan finding N" — the caller told its id was wrong for a
// problem on this side of the wire.
func TestScanIgnoreMissAndFailureAreDifferentStatuses(t *testing.T) {
	h, store := seededHandler(t)
	ctx := context.Background()

	res, err := store.DB().ExecContext(ctx, `
		INSERT INTO scan_findings (rule_id, description, entity_type, natural_key, match_redacted, line_number, scanned_at)
		VALUES ('slack-bot-token', 'Slack token', 'message', 'message/s1', 'xoxb…al', 3, '2026-07-10T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}

	code, env := post(t, h, fmt.Sprintf("/api/v1/scan/%d/ignore", id+10_000), `{"ignored":true}`, nil)
	if code != http.StatusNotFound {
		t.Errorf("unknown finding = %d, want 404 (%s)", code, env.Error)
	}
	code, env = post(t, h, fmt.Sprintf("/api/v1/scan/%d/ignore", id), `{"ignored":true}`, nil)
	if code != http.StatusOK {
		t.Fatalf("ignore = %d (%s)", code, env.Error)
	}

	// A store that cannot answer is a 500 — and must not claim the finding
	// is missing, which would send the caller looking at its own id.
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	code, env = post(t, h, fmt.Sprintf("/api/v1/scan/%d/ignore", id), `{"ignored":true}`, nil)
	if code != http.StatusInternalServerError {
		t.Errorf("store failure = %d, want 500 (%s)", code, env.Error)
	}
	if strings.Contains(env.Error, "scan finding") {
		t.Errorf("500 body blames the finding id: %q", env.Error)
	}
}

// TestListContractsEncodeEmptyAsArray: every list surface answers []
// on the wire when nothing matches — never null — so consumers of the
// versioned envelope need no null-vs-empty branch.
func TestListContractsEncodeEmptyAsArray(t *testing.T) {
	h := emptyHandler(t)
	lists := []string{
		"/api/v1/sessions",
		"/api/v1/commands",
		"/api/v1/history",
		"/api/v1/usage",
		"/api/v1/search?query=nothing-matches-this",
		"/api/v1/artifacts",
		"/api/v1/scan",
		"/api/v1/blocks",
	}
	for _, path := range lists {
		code, body := rawGet(t, h, path)
		if code != 200 {
			t.Errorf("GET %s = %d, want 200 (%s)", path, code, body)
			continue
		}
		if !strings.Contains(body, `"data":[]`) {
			t.Errorf("GET %s body = %s, want \"data\":[]", path, strings.TrimSpace(body))
		}
	}
}

// TestParamContractsRejectMalformedInput: malformed integers, negative
// limits/offsets, and malformed dates are 400s — not silently coerced
// zero defaults that would change the query's meaning.
func TestParamContractsRejectMalformedInput(t *testing.T) {
	h := emptyHandler(t)
	bad := []string{
		"/api/v1/sessions?limit=abc",
		"/api/v1/sessions?offset=-1",
		"/api/v1/sessions?since=notadate",
		"/api/v1/sessions?until=2026-13-99",
		"/api/v1/commands?limit=-5",
		"/api/v1/history?limit=abc",
		"/api/v1/commands?since=yesterday",
		"/api/v1/usage?limit=abc",
		"/api/v1/usage?since=07/01/2026",
		"/api/v1/search?query=x&limit=abc",
		"/api/v1/artifacts?offset=abc",
		"/api/v1/blocks?limit=-1",
		"/api/v1/sessions/claude-code/x/transcript?from_seq=abc",
		"/api/v1/sessions/claude-code/x/tools?from_seq=-2",
		"/api/v1/sessions/claude-code/x/tools/abc",
		"/api/v1/sessions/claude-code/x/tools?compact=yes",
		"/api/v1/sessions/claude-code/x/transcript?full=maybe",
		"/api/v1/scan?ignored=2",
	}
	for _, path := range bad {
		code, body := rawGet(t, h, path)
		if code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400 (%s)", path, code, strings.TrimSpace(body))
		}
	}

	// The zero and valid forms stay accepted.
	good := []string{
		"/api/v1/sessions?limit=0&offset=0",
		"/api/v1/sessions?since=2026-01-01&until=2026-02-01",
		"/api/v1/usage?group=day&limit=10",
	}
	for _, path := range good {
		if code, body := rawGet(t, h, path); code != 200 {
			t.Errorf("GET %s = %d, want 200 (%s)", path, code, strings.TrimSpace(body))
		}
	}
}

// TestOverCapLimitsAreRejected: a limit past an op's ceiling is a 400
// that names the ceiling, not a truncated 200. Capping silently told a
// caller asking for 2000 transcript entries that the session ended at
// 1000 — the same answer it would get if that were true.
func TestOverCapLimitsAreRejected(t *testing.T) {
	h := emptyHandler(t)
	overCap := map[string]int{
		"/api/v1/sessions?limit=501":                           500,
		"/api/v1/sessions/claude-code/x/transcript?limit=1001": 1000,
		"/api/v1/search?query=x&limit=101":                     100,
		"/api/v1/commands?limit=1001":                          1000,
		"/api/v1/blocks?limit=201":                             200,
	}
	for path, max := range overCap {
		code, body := rawGet(t, h, path)
		if code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400 (%s)", path, code, strings.TrimSpace(body))
			continue
		}
		if !strings.Contains(body, strconv.Itoa(max)) {
			t.Errorf("GET %s: 400 does not name the maximum %d: %s", path, max, strings.TrimSpace(body))
		}
	}

	// The ceilings themselves stay valid — the SPA's transcript page size
	// is exactly the transcript maximum, and its shell-history export
	// exactly the commands one.
	for _, path := range []string{
		"/api/v1/sessions?limit=500",
		"/api/v1/search?query=x&limit=100",
		"/api/v1/commands?limit=1000",
		"/api/v1/commands?format=zsh&limit=1000",
		"/api/v1/blocks?limit=200",
	} {
		if code, body := rawGet(t, h, path); code != 200 {
			t.Errorf("GET %s = %d, want 200 (%s)", path, code, strings.TrimSpace(body))
		}
	}
	// Uncapped ops keep honoring an explicit large page.
	for _, path := range []string{
		"/api/v1/artifacts?limit=5000",
		"/api/v1/history?limit=5000",
		"/api/v1/usage?limit=5000",
		"/api/v1/sessions/claude-code/x/tools?limit=5000",
	} {
		if code, body := rawGet(t, h, path); code == http.StatusBadRequest {
			t.Errorf("GET %s = 400, want the limit honored (%s)", path, strings.TrimSpace(body))
		}
	}
}
