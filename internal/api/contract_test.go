package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

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
	return Handler(query.New(store, table), nil, nil, nil, nil)
}

func rawGet(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
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
