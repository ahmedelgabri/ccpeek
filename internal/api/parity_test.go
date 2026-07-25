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

// The Origin guard only ever fires when a browser sends Origin. Under DNS
// rebinding the attacker's page is SAME origin with the server, so no
// Origin header exists and every read would answer. The Host header is
// what still carries the attacker's name.
func TestLoopbackOnlyRejectsRebinding(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secret transcript"))
	})
	h := LoopbackOnly(inner)

	allowed := []string{
		"127.0.0.1:3000",
		"127.0.0.1",
		"localhost:3000",
		"localhost",
		"[::1]:3000",
		"::1",
		"127.0.0.2:3000", // the whole 127/8 block is loopback
		"[::ffff:127.0.0.1]:3000",
	}
	for _, host := range allowed {
		req := httptest.NewRequest("GET", "http://example/api/v1/sessions", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, rec.Code)
		}
	}

	rejected := []string{
		"evil.example:3000",      // classic rebinding target
		"evil.localhost:3000",    // .localhost is not reliably loopback
		"notlocalhost",           // no substring matching
		"localhost.evil.example", // nor prefix matching
		"192.168.1.5:3000",       // a LAN address the server may also answer on
		"0.0.0.0:3000",
		"", // an absent Host is not implicitly local
	}
	for _, host := range rejected {
		req := httptest.NewRequest("GET", "http://example/api/v1/sessions", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("Host %q = %d, want 403", host, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "secret transcript") {
			t.Errorf("Host %q reached the inner handler", host)
		}
	}
}

// The guard has to cover reads, not just the two mutating endpoints —
// that is the entire point, since rebinding sends no Origin header.
func TestLoopbackOnlyCoversReadsWithoutOrigin(t *testing.T) {
	h := LoopbackOnly(Handler(nil, nil, nil, nil, nil))
	for _, path := range []string{
		"/api/v1/sessions",
		"/api/v1/search?q=x",
		"/api/v1/scan",
		"/api/v1/health",
	} {
		req := httptest.NewRequest("GET", "http://evil.example"+path, nil)
		req.Host = "evil.example:3000" // no Origin header, exactly as a same-origin fetch sends
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a rebound host = %d, want 403", path, rec.Code)
		}
	}
}
