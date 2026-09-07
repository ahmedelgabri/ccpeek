package api

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

func TestReadinessDoesNotCallAnIncompletePassComplete(t *testing.T) {
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`INSERT INTO ingest_runs(mode,status,started_at,parse_failures) VALUES('incremental','partial','now',1)`); err != nil {
		t.Fatal(err)
	}
	// This process has finished its own startup. The durable archive state
	// must still hold readiness for work performed by another process.
	h := Handler(query.New(store, nil), Deps{Ready: func() bool { return true }})
	for _, status := range []string{"partial", "failed", "running"} {
		if _, err := store.DB().Exec(`UPDATE ingest_runs SET status=?`, status); err != nil {
			t.Fatal(err)
		}
		code, env := get(t, h, "/api/v1/ready")
		want := "index-partial"
		if status == "running" {
			want = "index-unfinished"
		}
		if code != http.StatusServiceUnavailable || env.Data.(map[string]any)["status"] != want {
			t.Fatalf("%s readiness=%d data=%v", status, code, env.Data)
		}
		for _, path := range []string{"/api/v1/health", "/api/v1/archive-status", "/api/v1/sessions"} {
			if code, _ := get(t, h, path); code != http.StatusOK {
				t.Fatalf("%s during %s=%d", path, status, code)
			}
		}
	}
	if _, err := store.DB().Exec(`UPDATE ingest_runs SET status='ok'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, h, "/api/v1/ready"); code != http.StatusOK {
		t.Fatalf("repaired readiness=%d", code)
	}
}
