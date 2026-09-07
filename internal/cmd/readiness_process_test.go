package cmd

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/db"
)

// This child commits a running row while holding the maintenance lock. The
// parent kills it without FinishRun or Close, like an interrupted index pass.
func TestUnfinishedRunWriterProcess(t *testing.T) {
	path := os.Getenv("CCPEEK_TEST_UNFINISHED_RUN_DB")
	if path == "" {
		return
	}
	ctx := context.Background()
	store, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, unlock, err := store.LockMaintenance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := store.StartRun(ctx, "incremental", "[]"); err != nil {
		t.Fatal(err)
	}
	fmt.Println("RUNNING")
	time.Sleep(30 * time.Second)
	t.Fatal("parent did not stop the writer")
}

func TestSkipIndexReadinessDuringAndAfterAnExternalInterruptedRun(t *testing.T) {
	cmd := startupCommand(t)
	if err := cmd.Flags().Set("skip-index", "true"); err != nil {
		t.Fatal(err)
	}
	path, _ := cmd.Flags().GetString("index-file")
	ctx := context.Background()
	store, err := db.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for key, value := range map[string]string{"migrated_at": "test", metaV1ImportState: "no-legacy-db"} {
		if err := store.SetMeta(ctx, key, value); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(executable, "-test.run=^TestUnfinishedRunWriterProcess$")
	child.Env = append(os.Environ(), "CCPEEK_TEST_UNFINISHED_RUN_DB="+path)
	child.Stderr = os.Stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	stop := sync.OnceFunc(func() { _ = child.Process.Kill(); _ = child.Wait() })
	t.Cleanup(stop)
	started := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(stdout); started <- scanner.Scan() && scanner.Text() == "RUNNING" }()
	select {
	case ok := <-started:
		if !ok {
			t.Fatal("external writer did not start")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("external writer startup timed out")
	}
	url, _ := startBrowserRun(t, cmd)
	// Local initialization is complete and normal queries still work. Readiness
	// must consult the committed row, not just this server's Ready callback.
	startupResponse(t, url+"/api/v1/health", http.StatusOK, `"indexing":false`)
	startupResponse(t, url+"/api/v1/sessions", http.StatusOK, `"data":[]`)
	startupResponse(t, url+"/api/v1/ready", http.StatusServiceUnavailable, `"status":"index-unfinished"`)
	stop()
	startupResponse(t, url+"/api/v1/ready", http.StatusServiceUnavailable, `"status":"index-unfinished"`)
	// A clean derived state or a released lock is not evidence of a completed
	// pass. Only a subsequent completed run restores archive readiness.
	if dirty, err := store.DerivedDirty(ctx); err != nil || dirty {
		t.Fatalf("dirty=%v err=%v", dirty, err)
	}
	recoveryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	recoveryCtx, unlock, err := store.LockMaintenance(recoveryCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	runID, err := store.StartRun(recoveryCtx, "incremental", "[]")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(recoveryCtx, runID, "ok", time.Now(), db.RunCounts{}, ""); err != nil {
		t.Fatal(err)
	}
	startupResponse(t, url+"/api/v1/ready", http.StatusOK, `"status":"ready"`)
}
