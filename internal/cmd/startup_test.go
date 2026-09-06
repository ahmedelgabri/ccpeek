package cmd

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/webui"
	"github.com/spf13/cobra"
)

func startupCommand(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := newRootTestCommand(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	for name, value := range map[string]string{"index-only": "false", "open": "true", "port": strconv.Itoa(port)} {
		if err := cmd.Flags().Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	cmd.Flags().String("index-file", filepath.Join(t.TempDir(), "archive.db"), "")
	return cmd
}

func startBrowserRun(t *testing.T, cmd *cobra.Command) (string, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)
	opened := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- runWithBrowser(cmd, func(url string) { opened <- url }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("startup did not stop on cancellation")
		}
	})
	select {
	case url := <-opened:
		return url, cancel
	case <-time.After(3 * time.Second):
		t.Fatal("browser launch waited for archive initialization")
		return "", cancel
	}
}

func startupResponse(t *testing.T, url string, status int, contains string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	var body []byte
	var code int
	var err error
	for time.Now().Before(deadline) {
		var res *http.Response
		res, err = client.Get(url)
		if err == nil {
			code = res.StatusCode
			body, err = io.ReadAll(res.Body)
			res.Body.Close()
			if err == nil && code == status && strings.Contains(string(body), contains) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: status=%d body=%s err=%v; want %d containing %q", url, code, body, err, status, contains)
}

func TestRunOpensBrowserWhileArchiveIsBusy(t *testing.T) {
	for _, mode := range []string{"current", "migration", "cancel-migration"} {
		t.Run(mode, func(t *testing.T) {
			cmd := startupCommand(t)
			path, _ := cmd.Flags().GetString("index-file")
			store, err := db.Open(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			if err := store.SetMeta(context.Background(), "migrated_at", "test"); err != nil {
				t.Fatal(err)
			}
			if mode != "current" {
				// Undo migration 17 so startup has real schema work to do.
				if _, err := store.DB().Exec(`DROP TABLE dirty_sessions; UPDATE meta SET value = '16' WHERE key = 'schema_version'`); err != nil {
					t.Fatal(err)
				}
			}
			_, release, err := store.LockMaintenance(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			unlock := sync.OnceFunc(release)
			t.Cleanup(unlock)
			url, cancel := startBrowserRun(t, cmd)
			if webui.Embedded() {
				startupResponse(t, url+"/", http.StatusOK, `id="root"`)
			} else {
				startupResponse(t, url+"/", http.StatusNotImplemented, "API-only")
			}
			if mode == "current" {
				startupResponse(t, url+"/api/v1/sessions", http.StatusOK, `"data":[]`)
				startupResponse(t, url+"/api/v1/ready", http.StatusServiceUnavailable, `"status":"indexing"`)
			} else {
				startupResponse(t, url+"/api/v1/health", http.StatusOK, `"initializing":true`)
				startupResponse(t, url+"/api/v1/ready", http.StatusServiceUnavailable, `"status":"initializing"`)
				startupResponse(t, url+"/api/v1/sessions", http.StatusServiceUnavailable, `archive is initializing`)
			}
			if mode == "cancel-migration" {
				cancel() // Cleanup joins startup before releasing the held lock.
				return
			}

			// Subscribe BEFORE initialization completes; swapping the API must
			// not strand the page on an old broadcaster or block on this stream.
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			req, _ := http.NewRequestWithContext(ctx, "GET", url+"/api/v1/events", nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			reader := bufio.NewScanner(res.Body)
			if !reader.Scan() || reader.Text() != "event: hello" {
				t.Fatal("SSE did not start during initialization")
			}
			unlock()
			startupResponse(t, url+"/api/v1/ready", http.StatusOK, `"status":"ready"`)
			startupResponse(t, url+"/api/v1/sessions", http.StatusOK, `"data":[]`)
			for reader.Scan() {
				if reader.Text() == "event: changed" {
					return
				}
			}
			t.Fatalf("no SSE notification after initialization: %v", reader.Err())
		})
	}
}

func TestRunShowsInitializationFailure(t *testing.T) {
	cmd := startupCommand(t)
	path, _ := cmd.Flags().GetString("index-file")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	url, _ := startBrowserRun(t, cmd)
	startupResponse(t, url+"/api/v1/health", http.StatusOK, `"state":"failed"`)
	startupResponse(t, url+"/api/v1/ready", http.StatusServiceUnavailable, "Archive initialization failed")
	startupResponse(t, url+"/api/v1/sessions", http.StatusServiceUnavailable, `"error":"archive initialization failed"`)
}

func TestRunRejectsEmptyArchiveFlagsBeforeBrowser(t *testing.T) {
	for _, flag := range []string{"data-file", "index-file"} {
		t.Run(flag, func(t *testing.T) {
			cmd := startupCommand(t)
			if err := cmd.Flags().Set(flag, ""); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			cmd.SetContext(ctx)
			err := runWithBrowser(cmd, func(string) { t.Error("opened browser with invalid flags") })
			if err == nil || !strings.Contains(err.Error(), "--"+flag) {
				t.Fatalf("invalid --%s: %v", flag, err)
			}
		})
	}
}

func TestRunChecksPortBeforeOpeningArchive(t *testing.T) {
	cmd := startupCommand(t)
	n, _ := cmd.Flags().GetInt("port")
	port := strconv.Itoa(n)
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	err = runWithBrowser(cmd, func(string) { t.Error("opened browser on an occupied port") })
	if err == nil || !strings.Contains(err.Error(), "listening on") {
		t.Fatalf("bind failure: %v", err)
	}
	path, _ := cmd.Flags().GetString("index-file")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("opened archive before checking port: %v", err)
	}
}
