package server

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/store"
)

func TestListenAndServeGracefulShutdown(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ListenAndServe(ctx, "127.0.0.1:0", db, t.TempDir(), "", false, 30*time.Second, false)
	}()

	// Give the server a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context to trigger graceful shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error after graceful shutdown, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down within 3 seconds")
	}
}

func TestListenAndServeContextCancelled(t *testing.T) {
	db, err := store.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Already-cancelled context should shut down immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = ListenAndServe(ctx, "127.0.0.1:0", db, t.TempDir(), "", false, 30*time.Second, false)
	// Should return nil (ErrServerClosed is treated as clean shutdown)
	if err != nil && err != http.ErrServerClosed {
		t.Fatalf("expected nil or ErrServerClosed, got: %v", err)
	}
}
