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
	"github.com/ahmedelgabri/ccpeek/internal/ops"
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
	return Handler(query.New(store, table), nil, nil, nil, nil)
}

func get(t *testing.T, h http.Handler, path string) (int, ops.Envelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env ops.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("GET %s: bad JSON: %v\n%s", path, err, rec.Body.String())
	}
	if env.Schema != ops.PayloadSchema {
		t.Errorf("GET %s: schema = %q", path, env.Schema)
	}
	return rec.Code, env
}

// TestHealthV1Import proves a failed legacy import stays visible on the
// health surface — and that handlers without the hook omit the field.
func TestHealthV1Import(t *testing.T) {
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "v2.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	table, err := pricing.Embedded()
	if err != nil {
		t.Fatal(err)
	}

	h := Handler(query.New(store, table), nil, nil, nil, func() V1ImportStatus {
		return V1ImportStatus{State: "failed", Error: "reading v1 database: file is not a database"}
	})
	code, env := get(t, h, "/api/v1/health")
	if code != 200 {
		t.Fatalf("health = %d", code)
	}
	data, _ := env.Data.(map[string]any)
	v1, _ := data["v1Import"].(map[string]any)
	if v1 == nil {
		t.Fatalf("health payload lacks v1Import: %v", env.Data)
	}
	if v1["state"] != "failed" || v1["error"] != "reading v1 database: file is not a database" {
		t.Errorf("v1Import = %v", v1)
	}

	// Readiness holds at 503 while the import is failed — partial history
	// must not read as ready — with a status distinct from "indexing".
	code, env = get(t, h, "/api/v1/ready")
	if code != http.StatusServiceUnavailable {
		t.Errorf("ready with failed import = %d, want 503", code)
	}
	if d, _ := env.Data.(map[string]any); d["status"] != "v1-import-failed" {
		t.Errorf("ready status = %v, want v1-import-failed", env.Data)
	}

	hOK := Handler(query.New(store, table), nil, nil, nil, func() V1ImportStatus {
		return V1ImportStatus{State: "success", ImportedAt: "2026-07-22T00:00:00Z"}
	})
	if code, _ := get(t, hOK, "/api/v1/ready"); code != 200 {
		t.Errorf("ready with successful import = %d, want 200", code)
	}

	h2 := Handler(query.New(store, table), nil, nil, nil, nil)
	_, env2 := get(t, h2, "/api/v1/health")
	data2, _ := env2.Data.(map[string]any)
	if _, ok := data2["v1Import"]; ok {
		t.Errorf("v1Import present without a hook: %v", env2.Data)
	}
	if code, _ := get(t, h2, "/api/v1/ready"); code != 200 {
		t.Errorf("ready without a hook = %d, want 200", code)
	}
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

	// Tool list rows never carry diff excerpts; the per-call detail does.
	code, env = get(t, h, "/api/v1/sessions/claude-code/11111111-aaaa-bbbb-cccc-111111111111/tools")
	if code != 200 {
		t.Fatalf("tools = %d (%s)", code, env.Error)
	}
	toolRows, _ := env.Data.([]any)
	if len(toolRows) == 0 {
		t.Fatal("tools list empty")
	}
	for _, r := range toolRows {
		row := r.(map[string]any)
		if _, has := row["old"]; has {
			t.Errorf("list row carries old excerpt: %v", row)
		}
		if _, has := row["new"]; has {
			t.Errorf("list row carries new excerpt: %v", row)
		}
	}
	code, env = get(t, h, "/api/v1/sessions/claude-code/11111111-aaaa-bbbb-cccc-111111111111/tools/1")
	if code != 200 {
		t.Fatalf("tool detail = %d (%s)", code, env.Error)
	}
	toolDetail, _ := env.Data.(map[string]any)
	if toolDetail["kind"] == "file_edit" && toolDetail["old"] == nil && toolDetail["new"] == nil {
		t.Errorf("edit detail lacks excerpts: %v", toolDetail)
	}
	if code, _ = get(t, h, "/api/v1/sessions/claude-code/11111111-aaaa-bbbb-cccc-111111111111/tools/abc"); code != 400 {
		t.Errorf("malformed tool seq = %d, want 400", code)
	}
	if code, _ = get(t, h, "/api/v1/sessions/claude-code/11111111-aaaa-bbbb-cccc-111111111111/tools/999"); code != 404 {
		t.Errorf("unknown tool seq = %d, want 404", code)
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

	code, env = get(t, h, "/api/v1/search?query=rate+limiting&limit=5")
	if code != 200 {
		t.Fatalf("search = %d", code)
	}
	if hits, _ := env.Data.([]any); len(hits) == 0 {
		t.Error("search empty")
	}
}

// TestArtifactRawIsSandboxed: the raw endpoint serves agent-produced
// (untrusted) bytes — HTML must carry a CSP sandbox so a DIRECT
// navigation cannot run scripts with the app origin, and nothing may be
// MIME-sniffed into something executable.
func TestArtifactRawIsSandboxed(t *testing.T) {
	h := newHandler(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/artifacts/claude-code/plan/rate-limit-plan.md/raw", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("raw plan = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("plan content-type = %q, want text/plain", ct)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("raw response missing nosniff")
	}

	// The HTML kind must be sandboxed. The fixture corpus ships a usage
	// report; assert the header contract on it.
	req = httptest.NewRequest(http.MethodGet,
		"/api/v1/artifacts/claude-code/usage_report/report.html/raw", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("raw usage report = %d", rec.Code)
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != "sandbox allow-scripts" {
		t.Errorf("usage report CSP = %q, want sandbox allow-scripts", csp)
	}
}
