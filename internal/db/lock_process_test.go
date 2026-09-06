package db

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestMaintenanceLockAcrossProcesses(t *testing.T) {
	if path := os.Getenv("CCPEEK_TEST_LOCK_PATH"); path != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		store, err := Open(ctx, path)
		if err == nil {
			store.Close()
			t.Fatal("opened archive while another process held its maintenance lock")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lock wait: %v", err)
		}
		return
	}
	store, path := openTemp(t)
	_, unlock, err := store.LockMaintenance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	command := exec.Command(os.Args[0], "-test.run=^TestMaintenanceLockAcrossProcesses$")
	command.Env = append(os.Environ(), "CCPEEK_TEST_LOCK_PATH="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child: %v\n%s", err, output)
	}
}
