package api

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/ops"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

func post(t *testing.T, h http.Handler, path, body string, headers map[string]string) (int, ops.Envelope) {
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
	var env ops.Envelope
	_ = envUnmarshal(rec.Body.Bytes(), &env)
	return rec.Code, env
}

func envUnmarshal(b []byte, env *ops.Envelope) error {
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

// TestRouteParamParity is the parameter-level half of route parity, and
// the blind spot the name-level test above left open: the route table
// mapped /sessions to the `sessions` op while the handler underneath read
// "q" for the parameter the registry calls "query", and /transcript read
// "from" for "from_seq". An agent using the canonical names got an
// UNFILTERED answer with a 200.
//
// So each op route's handler must read EXACTLY the parameters the route
// accepts — the registry op's (minus those the pattern binds as path
// segments) plus the route's declared transport-only extras. A registry
// parameter HTTP never reads fails here, and so does a handler reading a
// name nothing declares.
func TestRouteParamParity(t *testing.T) {
	files := parsePackageFiles(t)
	handlerFor := routeHandlerNames(t, files)
	byOp := map[string]ops.Op{}
	for _, op := range ops.Registry() {
		byOp[op.Name] = op
	}

	for _, r := range Routes() {
		if r.Kind != "op" {
			continue
		}
		op, ok := byOp[r.Op]
		if !ok {
			continue // TestRouteRegistryParity reports the missing op
		}
		method, ok := handlerFor[r.Pattern]
		if !ok {
			t.Errorf("route %s is not in Handler's byPattern table", r.Pattern)
			continue
		}
		accepted := AcceptedParams(r, op)
		read := paramsReadBy(t, files, method)
		for _, name := range accepted {
			if !slices.Contains(read, name) {
				t.Errorf("%s: handler %s never reads %q — op %q declares it, so HTTP must answer to it",
					r.Pattern, method, name, op.Name)
			}
		}
		for _, name := range read {
			if !slices.Contains(accepted, name) {
				t.Errorf("%s: handler %s reads %q, which op %q does not declare and the route does not list in Extra",
					r.Pattern, method, name, op.Name)
			}
		}
	}
}

// parsePackageFiles parses the package's own sources — this test reads
// the handlers rather than only calling them, because "the handler reads
// this parameter" is not observable from a response.
func parsePackageFiles(t *testing.T) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no package sources found")
	}
	return files
}

// routeHandlerNames reads Handler's byPattern table: route pattern → the
// handler method serving it.
func routeHandlerNames(t *testing.T, files []*ast.File) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			if id, ok := assign.Lhs[0].(*ast.Ident); !ok || id.Name != "byPattern" {
				return true
			}
			lit, ok := assign.Rhs[0].(*ast.CompositeLit)
			if !ok {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.BasicLit)
				if !ok {
					continue
				}
				pattern, err := strconv.Unquote(key.Value)
				if err != nil {
					continue
				}
				if name := handlerMethodName(kv.Value); name != "" {
					out[pattern] = name
				}
			}
			return true
		})
	}
	if len(out) == 0 {
		t.Fatal("Handler's byPattern table not found — this test reads it to know which handler serves a route")
	}
	return out
}

// handlerMethodName pulls the method out of a table entry, including the
// wrapped form sameOriginOnly(h.setBudget).
func handlerMethodName(v ast.Expr) string {
	switch e := v.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.CallExpr:
		if len(e.Args) == 1 {
			return handlerMethodName(e.Args[0])
		}
	}
	return ""
}

// paramsReadBy lists the query parameters one handler reads: the literals
// it passes to the typed params helper. Reaching past that helper into
// r.URL.Query() would hide a parameter from this check, so it fails.
func paramsReadBy(t *testing.T, files []*ast.File, method string) []string {
	t.Helper()
	var read []string
	found := false
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != method {
				continue
			}
			found = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Str", "Int", "Bool":
					if _, ok := sel.X.(*ast.Ident); !ok || len(call.Args) != 1 {
						return true
					}
					lit, ok := call.Args[0].(*ast.BasicLit)
					if !ok {
						return true
					}
					if name, err := strconv.Unquote(lit.Value); err == nil {
						read = append(read, name)
					}
				case "Query":
					if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "URL" {
						t.Errorf("handler %s reads r.URL.Query() directly; read query parameters through params so the route's allowlist stays checkable", method)
					}
				}
				return true
			})
		}
	}
	if !found {
		t.Fatalf("no handler method %q in the package", method)
	}
	slices.Sort(read)
	return slices.Compact(read)
}

// TestOpRoutesRejectUnknownParams: every declared parameter answers, and
// every other name is a 400 that says which one. A parameter the server
// drops on the floor turns a narrow question into an archive-wide answer
// with no way for the caller to tell.
func TestOpRoutesRejectUnknownParams(t *testing.T) {
	h := newHandler(t)
	byOp := map[string]ops.Op{}
	for _, op := range ops.Registry() {
		byOp[op.Name] = op
	}

	for _, r := range Routes() {
		if r.Kind != "op" {
			continue
		}
		op, ok := byOp[r.Op]
		if !ok {
			continue
		}
		path := concretePath(r.Pattern)

		// Declared names are accepted. The value may still be rejected on
		// its own merits (a malformed date, an unknown group), so only the
		// unknown-parameter refusal is a failure here.
		for _, name := range AcceptedParams(r, op) {
			code, env := get(t, h, path+"?"+name+"=1")
			if code == http.StatusBadRequest && strings.Contains(env.Error, "unknown parameter") {
				t.Errorf("GET %s?%s=1 rejects a declared parameter: %s", path, name, env.Error)
			}
		}

		code, env := get(t, h, path+"?nonesuch=1")
		if code != http.StatusBadRequest {
			t.Errorf("GET %s?nonesuch=1 = %d, want 400", path, code)
		} else if !strings.Contains(env.Error, "nonesuch") {
			t.Errorf("GET %s?nonesuch=1: 400 does not name the parameter: %q", path, env.Error)
		}
	}
}

// concretePath fills a pattern's wildcards with real-shaped values.
// Whether they resolve does not matter: the parameter check runs before
// the handler, so a 404 body still carries the answer under test.
func concretePath(pattern string) string {
	path := strings.TrimPrefix(pattern, "GET ")
	for wildcard, value := range map[string]string{
		"{agent}": "claude-code",
		"{id}":    "x",
		"{kind}":  "plan",
		"{name}":  "y",
		"{seq}":   "1",
	} {
		path = strings.ReplaceAll(path, wildcard, value)
	}
	return path
}

// The pre-registry spellings are retired, not aliased: HTTP now answers
// only to the canonical names, and the old ones fail loudly instead of
// returning everything.
func TestRetiredParamSpellingsAreRejected(t *testing.T) {
	h := newHandler(t)
	for _, path := range []string{
		"/api/v1/sessions?q=rate",
		"/api/v1/commands?q=git",
		"/api/v1/history?q=rate",
		"/api/v1/search?q=rate",
		"/api/v1/sessions/claude-code/x/transcript?from=2",
	} {
		code, env := get(t, h, path)
		if code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400 (an ignored filter answers with everything)", path, code)
			continue
		}
		if !strings.Contains(env.Error, "valid:") {
			t.Errorf("GET %s: 400 does not name the accepted parameters: %q", path, env.Error)
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
	h := LoopbackOnly(Handler(nil, Deps{}))
	for _, path := range []string{
		"/api/v1/sessions",
		"/api/v1/search?query=x",
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

// Only caller-facing errors carry their message to the client. The
// wrapped internal errors name SQL fragments and absolute filesystem
// paths, and returning those verbatim on a 500 handed them to whoever
// asked — which matters more now that a rebound host is a real caller.
func TestInternalErrorsDoNotLeakDetail(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, fmt.Errorf("listing sessions: opening /Users/someone/.local/share/ccpeek/ccpeek2.db: permission denied"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, leaked := range []string{"/Users/someone", "ccpeek2.db", "listing sessions"} {
		if strings.Contains(body, leaked) {
			t.Errorf("500 response leaked %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, "internal error") {
		t.Errorf("500 response has no generic message: %s", body)
	}
}

// Caller mistakes and misses keep their detail — that is what makes them
// actionable.
func TestCallerFacingErrorsKeepTheirMessage(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		want   string
	}{
		{
			fmt.Errorf("%w: parameter limit=%q (want a non-negative integer)", query.ErrBadRequest, "abc"),
			http.StatusBadRequest, "limit",
		},
		{
			fmt.Errorf("%w: session claude-code/nope", query.ErrNotFound),
			http.StatusNotFound, "claude-code/nope",
		},
	} {
		rec := httptest.NewRecorder()
		writeError(rec, tc.err)
		if rec.Code != tc.status {
			t.Errorf("status = %d, want %d", rec.Code, tc.status)
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("body %q does not mention %q", rec.Body.String(), tc.want)
		}
	}
}

// The mutating endpoints bound their request bodies and reject unknown
// fields, so an oversized payload cannot make the server allocate and a
// client typo surfaces instead of being silently ignored.
func TestMutatingEndpointsBoundAndValidateBodies(t *testing.T) {
	h := newHandler(t)

	huge := `{"monthlyUSD": 1, "pad": "` + strings.Repeat("x", 64*1024) + `"}`
	code, _ := post(t, h, "PUT /api/v1/budget", huge, nil)
	if code != http.StatusBadRequest {
		t.Errorf("oversized body = %d, want 400", code)
	}

	// An unknown field is a 400 that names it, rather than a silent
	// no-op that stores nothing and reports success. (Go matches field
	// names case-insensitively, so only a genuinely different name is
	// unknown.)
	code, env := post(t, h, "PUT /api/v1/budget", `{"monthlyBudget": 25}`, nil)
	if code != http.StatusBadRequest {
		t.Errorf("unknown field = %d, want 400", code)
	} else if !strings.Contains(env.Error, "monthlyBudget") {
		t.Errorf("400 does not name the unknown field: %q", env.Error)
	}

	// Non-finite values are a caller mistake, not a stored budget.
	code, _ = post(t, h, "PUT /api/v1/budget", `{"monthlyUSD": -1}`, nil)
	if code != http.StatusBadRequest {
		t.Errorf("negative budget = %d, want 400", code)
	}

	// The valid shape still works.
	code, _ = post(t, h, "PUT /api/v1/budget", `{"monthlyUSD": 25}`, nil)
	if code != http.StatusOK {
		t.Errorf("valid budget = %d, want 200", code)
	}
}
