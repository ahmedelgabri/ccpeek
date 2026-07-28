package db

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ahmedelgabri/ccpeek/internal/canon"
)

// Seeds a corpus shaped like a real one: many sessions, several days,
// a couple of models.
func seedBig(t *testing.B, s *Store, sessions, msgsPerSession int) {
	ctx := context.Background()
	w, err := s.BeginWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	models := []string{"claude-sonnet-5", "claude-haiku-4-5"}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range sessions {
		id, err := w.UpsertSession(canon.Session{
			Agent: "claude-code", ExternalID: fmt.Sprintf("s%d", i),
			CWD: fmt.Sprintf("/home/u/p%d", i%50),
		}, "h")
		if err != nil {
			t.Fatal(err)
		}
		for j := range msgsPerSession {
			if err := w.InsertMessage(id, "claude-code", canon.Message{
				Seq: j, Role: canon.RoleAssistant, Model: models[j%len(models)],
				CreatedAt: base.AddDate(0, 0, i%30).Add(time.Duration(j) * time.Minute),
				Usage:     &canon.Usage{InputTokens: 100, OutputTokens: 20},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := s.RegenerateWorkspaces(ctx); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkRegenerateRollups(b *testing.B) {
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir()+"/v2.db")
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	seedBig(b, s, 4000, 25) // 100k usage rows, 4k sessions, 30 days, 2 models

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM message_usage`).Scan(&n); err != nil {
		b.Fatal(err)
	}
	b.Logf("corpus: %d usage rows", n)

	table := stubPricer{"claude-sonnet-5": {Input: 1e-6, Output: 1e-5}}
	b.ResetTimer()
	for b.Loop() {
		if err := s.RegenerateRollups(ctx, table); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	var days, daily int
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollup_session_days`).Scan(&days)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rollup_usage_daily`).Scan(&daily)
	b.Logf("wrote %d session-day rows, %d daily rows", days, daily)
}
