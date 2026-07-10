package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/adapters/claude"
	"github.com/ahmedelgabri/ccpeek/internal/adapters/pi"
	"github.com/ahmedelgabri/ccpeek/internal/canon"
	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/ingest"
	"github.com/ahmedelgabri/ccpeek/internal/pricing"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

func newHandler(t *testing.T) http.Handler {
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
	fixture := func(dir string) []string {
		p, err := filepath.Abs(filepath.Join("../../testdata/agents", dir))
		if err != nil {
			t.Fatal(err)
		}
		return []string{p}
	}
	runner := ingest.New(store, table, claude.New(), pi.New())
	if _, err := runner.Run(context.Background(), ingest.Options{
		ConfigRoots: map[canon.AgentSlug][]string{
			claude.Slug: fixture("claude-code"),
			pi.Slug:     fixture("pi"),
		},
		Getenv: func(string) string { return "" },
		Home:   "/nonexistent",
	}); err != nil {
		t.Fatal(err)
	}
	return Handler(query.New(store, table))
}

func get(t *testing.T, h http.Handler, path string) (int, envelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("GET %s: bad JSON: %v\n%s", path, err, rec.Body.String())
	}
	if env.Schema != payloadSchema {
		t.Errorf("GET %s: schema = %q", path, env.Schema)
	}
	return rec.Code, env
}

func TestEndpoints(t *testing.T) {
	h := newHandler(t)

	if code, _ := get(t, h, "/api/v1/health"); code != 200 {
		t.Errorf("health = %d", code)
	}

	code, env := get(t, h, "/api/v1/sessions?agent=claude-code")
	if code != 200 {
		t.Fatalf("sessions = %d", code)
	}
	sessions, ok := env.Data.([]any)
	if !ok || len(sessions) != 3 {
		t.Fatalf("sessions data = %T len %d, want 3", env.Data, len(sessions))
	}

	code, env = get(t, h, "/api/v1/sessions/claude-code/11111111-aaaa-bbbb-cccc-111111111111")
	if code != 200 {
		t.Fatalf("session = %d (%s)", code, env.Error)
	}
	detail, _ := env.Data.(map[string]any)
	if detail["title"] != "Add rate limiting to the login endpoint" {
		t.Errorf("session title = %v", detail["title"])
	}
	if detail["costUSD"] == nil {
		t.Error("session missing costUSD")
	}

	code, _ = get(t, h, "/api/v1/sessions/claude-code/nope")
	if code != 404 {
		t.Errorf("missing session = %d, want 404", code)
	}

	code, env = get(t, h, "/api/v1/sessions/pi/9f8e7d6c-1111-2222-3333-444455556666/transcript?limit=3")
	if code != 200 {
		t.Fatalf("transcript = %d", code)
	}
	if msgs, _ := env.Data.([]any); len(msgs) != 3 {
		t.Errorf("transcript entries = %d, want 3 (limit)", len(msgs))
	}

	code, env = get(t, h, "/api/v1/usage?group=model")
	if code != 200 {
		t.Fatalf("usage = %d", code)
	}
	if rows, _ := env.Data.([]any); len(rows) == 0 {
		t.Error("usage empty")
	}

	code, _ = get(t, h, "/api/v1/usage?group=bogus")
	if code != 400 {
		t.Errorf("bogus group = %d, want 400", code)
	}

	code, env = get(t, h, "/api/v1/search?q=rate+limiting&limit=5")
	if code != 200 {
		t.Fatalf("search = %d", code)
	}
	if hits, _ := env.Data.([]any); len(hits) == 0 {
		t.Error("search empty")
	}
}
