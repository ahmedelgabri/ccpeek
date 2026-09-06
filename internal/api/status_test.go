package api

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/ahmedelgabri/ccpeek/internal/query"
)

func TestReadinessDoesNotCallAPartialPassComplete(t *testing.T) {
	store, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`INSERT INTO ingest_runs(mode,status,started_at,parse_failures) VALUES('incremental','partial','now',1)`); err != nil {
		t.Fatal(err)
	}
	h := Handler(query.New(store, nil), Deps{})
	if code, _ := get(t, h, "/api/v1/ready"); code != http.StatusServiceUnavailable {
		t.Fatalf("partial readiness=%d", code)
	}
	if code, _ := get(t, h, "/api/v1/archive-status"); code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if _, err := store.DB().Exec(`UPDATE ingest_runs SET status='ok'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, h, "/api/v1/ready"); code != http.StatusOK {
		t.Fatalf("repaired readiness=%d", code)
	}
}
