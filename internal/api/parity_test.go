package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/ops"
)

func post(t *testing.T, h http.Handler, path, body string, headers map[string]string) (int, envelope) {
	t.Helper()
	method := http.MethodPost
	if strings.HasPrefix(path, "PUT ") {
		method = http.MethodPut
		path = strings.TrimPrefix(path, "PUT ")
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env envelope
	_ = envUnmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env
}

func envUnmarshal(b []byte, env *envelope) error {
	return jsonUnmarshal(b, env)
}

func TestArtifactEndpoints(t *testing.T) {
	h := newHandler(t)

	code, env := get(t, h, "/api/v1/artifacts?kind=plan")
	if code != 200 {
		t.Fatalf("artifacts = %d", code)
	}
	list, _ := env.Data.([]any)
	if len(list) != 1 {
		t.Fatalf("plan artifacts = %d, want 1", len(list))
	}
	name := list[0].(map[string]any)["name"].(string)

	code, env = get(t, h, "/api/v1/artifacts/claude-code/plan/"+name)
	if code != 200 {
		t.Fatalf("artifact = %d (%s)", code, env.Error)
	}
	detail := env.Data.(map[string]any)
	html, _ := detail["contentHTML"].(string)
	if !strings.Contains(html, "<h1") {
		t.Errorf("plan not rendered to HTML: %q", html)
	}

	if code, _ := get(t, h, "/api/v1/artifacts/claude-code/plan/nope.md"); code != 404 {
		t.Errorf("missing artifact = %d, want 404", code)
	}
}

func TestScanAndBudgetEndpoints(t *testing.T) {
	h := newHandler(t)

	// No findings yet (scan not run in this fixture ingest).
	code, _ := get(t, h, "/api/v1/scan")
	if code != 200 {
		t.Fatalf("scan = %d", code)
	}

	// Budget round-trip.
	code, env := post(t, h, "PUT /api/v1/budget", `{"monthlyUSD": 25}`, nil)
	if code != 200 {
		t.Fatalf("set budget = %d (%s)", code, env.Error)
	}
	code, env = get(t, h, "/api/v1/budget")
	if code != 200 {
		t.Fatal(code)
	}
	b := env.Data.(map[string]any)
	if b["monthlyUSD"].(float64) != 25 {
		t.Errorf("budget = %v", b["monthlyUSD"])
	}
	if _, ok := b["spentUSD"]; !ok {
		t.Error("budget missing spentUSD")
	}

	// Cross-origin mutation is rejected; loopback origins pass.
	code, _ = post(t, h, "PUT /api/v1/budget", `{"monthlyUSD": 1}`,
		map[string]string{"Origin": "https://evil.example"})
	if code != 403 {
		t.Errorf("cross-origin put = %d, want 403", code)
	}
	code, _ = post(t, h, "PUT /api/v1/budget", `{"monthlyUSD": 30}`,
		map[string]string{"Origin": "http://localhost:3000"})
	if code != 200 {
		t.Errorf("loopback-origin put = %d, want 200", code)
	}
}

func TestBlocksEndpoint(t *testing.T) {
	h := newHandler(t)
	code, env := get(t, h, "/api/v1/blocks?limit=10")
	if code != 200 {
		t.Fatalf("blocks = %d", code)
	}
	blocks, _ := env.Data.([]any)
	if len(blocks) == 0 {
		t.Fatal("no blocks")
	}
	first := blocks[0].(map[string]any)
	for _, key := range []string{"start", "end", "tokens", "costUSD"} {
		if _, ok := first[key]; !ok {
			t.Errorf("block missing %s", key)
		}
	}
}

// jsonUnmarshal indirection keeps the helper header tidy.
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// TestRouteRegistryParity: every classified "op" route names a real
// registry operation, every registry operation is reachable over HTTP,
// and the transport/write remainder is the enumerated set — so a new
// endpoint cannot appear without either a registry op or an explicit
// transport classification.
func TestRouteRegistryParity(t *testing.T) {
	opsByName := map[string]bool{}
	for _, op := range ops.Registry() {
		opsByName[op.Name] = true
	}
	coveredOps := map[string]bool{}
	for _, r := range Routes() {
		switch r.Kind {
		case "op":
			if !opsByName[r.Op] {
				t.Errorf("route %s claims registry op %q, which does not exist", r.Pattern, r.Op)
			}
			coveredOps[r.Op] = true
		case "transport", "write":
			if r.Op != "" {
				t.Errorf("route %s is %s but names op %q", r.Pattern, r.Kind, r.Op)
			}
		default:
			t.Errorf("route %s has unknown kind %q", r.Pattern, r.Kind)
		}
	}
	for name := range opsByName {
		if !coveredOps[name] {
			t.Errorf("registry op %q has no HTTP route", name)
		}
	}
}
