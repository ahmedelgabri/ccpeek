package cmd

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func stubAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("api payload"))
	})
}

// The Host guard has to sit in front of the assembled layout, not inside
// the API mux — the SPA and the legacy redirects are reachable from a
// rebound host too, and a redirect leaks the URL structure of the archive.
func TestServeHandlerRejectsNonLoopbackHost(t *testing.T) {
	h := buildServeHandler(stubAPI())

	paths := []string{
		"/api/v1/sessions",
		"/",
		"/sessions/claude-code/abc",
		"/projects/dir/11111111-aaaa-bbbb-cccc-111111111111/",
		"/v2/usage",
	}
	for _, p := range paths {
		req := httptest.NewRequest("GET", "http://evil.example"+p, nil)
		req.Host = "evil.example:3000"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s from a rebound host = %d, want 403", p, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "api payload") {
			t.Errorf("%s reached the API from a rebound host", p)
		}
	}
}

// …and it must not get in the way of the real thing.
func TestServeHandlerAllowsLoopback(t *testing.T) {
	h := buildServeHandler(stubAPI())

	for _, host := range []string{"127.0.0.1:3000", "localhost:3000", "[::1]:3000"} {
		req := httptest.NewRequest("GET", "http://x/api/v1/sessions", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "api payload" {
			t.Errorf("Host %q = %d %q, want 200 api payload", host, rec.Code, rec.Body.String())
		}
	}

	// Legacy redirects still work from loopback.
	req := httptest.NewRequest("GET", "http://x/projects/dir/session-id/", nil)
	req.Host = "127.0.0.1:3000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("legacy redirect = %d, want 301", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/sessions/claude-code/session-id" {
		t.Errorf("Location = %q", got)
	}
}

// The listener is bound before anything observable depends on the server
// being up. --open used to launch the browser on the line before
// ListenAndServe, so a fast browser could hit connection refused on the
// tab ccpeek had just opened; binding first also surfaces "address
// already in use" before a window appears.
func TestListenBindsBeforeServing(t *testing.T) {
	ctx := context.Background()

	ln, err := listen(ctx, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// The port is reachable the moment listen returns, before serve runs:
	// a connection is accepted by the kernel backlog.
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("the bound port refused a connection before serve: %v", err)
	}
	conn.Close()

	// A second bind of the same address fails — the error a user gets
	// before, not after, a browser window opens.
	if dup, err := listen(ctx, ln.Addr().String()); err == nil {
		dup.Close()
		t.Error("binding an in-use address succeeded; the conflict would surface later")
	}
}

// serve takes the already-bound listener and shuts down with the context.
func TestServeUsesTheGivenListenerAndShutsDown(t *testing.T) {
	ln, err := listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, stubAPI()) }()

	// The handler answers on the address we bound.
	var res *http.Response
	for range 50 {
		res, err = http.Get("http://" + addr + "/api/v1/sessions")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatalf("serve never answered on %s: %v", addr, err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "api payload" {
		t.Errorf("body = %q", body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v, want nil on graceful shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("serve did not shut down with the context")
	}
}
