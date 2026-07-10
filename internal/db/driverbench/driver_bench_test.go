// Package driverbench compares the CGO SQLite driver (mattn/go-sqlite3)
// against the pure-Go driver (modernc.org/sqlite) on ccpeek-shaped
// workloads: bulk ingest of session/message/usage rows, FTS5 indexing and
// MATCH queries, and dashboard-style aggregates.
//
// Run with:
//
//	CGO_ENABLED=1 go test -tags sqlite_fts5 -bench . -benchtime 3x ./internal/db/driverbench
//
// Results feed the driver decision in docs/adr/0001-sqlite-driver.md.
package driverbench

import (
	"database/sql"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3" // driver "sqlite3" (CGO)
	_ "modernc.org/sqlite"          // driver "sqlite" (pure Go)
)

const (
	sessionCount        = 100
	messagesPerSession  = 120
	wordsPerMessage     = 80
	ftsQueryIterations  = 50
	aggrQueryIterations = 50
)

var vocabulary = []string{
	"session", "adapter", "ingest", "sqlite", "token", "cache", "migration",
	"schema", "workspace", "transcript", "usage", "pricing", "rollup",
	"claude", "codex", "opencode", "cursor", "search", "snippet", "artifact",
	"relation", "evidence", "budget", "dashboard", "timeline", "secret",
	"scan", "finding", "export", "command", "shell", "history", "branch",
	"parent", "sidechain", "compaction", "fork", "resume", "cost", "model",
}

type driverSpec struct {
	name string // sub-benchmark label
	drv  string // database/sql driver name
	dsn  func(path string) string
}

var drivers = []driverSpec{
	{
		name: "mattn",
		drv:  "sqlite3",
		dsn: func(path string) string {
			return "file:" + path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
		},
	},
	{
		name: "modernc",
		drv:  "sqlite",
		dsn: func(path string) string {
			return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
		},
	},
}

const benchSchema = `
CREATE TABLE sessions (
	id INTEGER PRIMARY KEY,
	external_id TEXT NOT NULL UNIQUE,
	title TEXT,
	created_at TEXT NOT NULL,
	modified_at TEXT NOT NULL
);
CREATE TABLE messages (
	id INTEGER PRIMARY KEY,
	session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	content TEXT NOT NULL
);
CREATE INDEX idx_messages_session ON messages(session_id, seq);
CREATE TABLE message_usage (
	message_id INTEGER PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
	input_tokens INTEGER NOT NULL,
	output_tokens INTEGER NOT NULL,
	cache_read_tokens INTEGER NOT NULL,
	cache_write_tokens INTEGER NOT NULL
);
CREATE VIRTUAL TABLE msg_fts USING fts5(text_content);
`

func message(rng *rand.Rand) string {
	words := make([]byte, 0, wordsPerMessage*8)
	for i := 0; i < wordsPerMessage; i++ {
		if i > 0 {
			words = append(words, ' ')
		}
		words = append(words, vocabulary[rng.Intn(len(vocabulary))]...)
	}
	return string(words)
}

func openBench(b *testing.B, spec driverSpec) *sql.DB {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.db")
	db, err := sql.Open(spec.drv, spec.dsn(path))
	if err != nil {
		b.Fatalf("open %s: %v", spec.name, err)
	}
	db.SetMaxOpenConns(1)
	b.Cleanup(func() { db.Close() })
	if _, err := db.Exec(benchSchema); err != nil {
		b.Fatalf("schema %s: %v", spec.name, err)
	}
	return db
}

// ingest writes the full synthetic corpus in one transaction per session,
// mirroring the per-source-file transaction plan for the v2 pipeline.
func ingest(b *testing.B, db *sql.DB) {
	b.Helper()
	rng := rand.New(rand.NewSource(42))
	msgID := 0
	for s := 0; s < sessionCount; s++ {
		tx, err := db.Begin()
		if err != nil {
			b.Fatalf("begin: %v", err)
		}
		res, err := tx.Exec(
			`INSERT INTO sessions (external_id, title, created_at, modified_at) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("sess-%04d", s), "bench session",
			fmt.Sprintf("2026-07-%02dT10:00:00Z", s%28+1),
			fmt.Sprintf("2026-07-%02dT11:00:00Z", s%28+1),
		)
		if err != nil {
			b.Fatalf("insert session: %v", err)
		}
		sid, _ := res.LastInsertId()

		msgStmt, err := tx.Prepare(`INSERT INTO messages (session_id, seq, role, created_at, content) VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			b.Fatalf("prepare messages: %v", err)
		}
		usageStmt, err := tx.Prepare(`INSERT INTO message_usage (message_id, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens) VALUES (?, ?, ?, ?, ?)`)
		if err != nil {
			b.Fatalf("prepare usage: %v", err)
		}
		ftsStmt, err := tx.Prepare(`INSERT INTO msg_fts (rowid, text_content) VALUES (?, ?)`)
		if err != nil {
			b.Fatalf("prepare fts: %v", err)
		}
		for m := 0; m < messagesPerSession; m++ {
			msgID++
			role := "user"
			if m%2 == 1 {
				role = "assistant"
			}
			content := message(rng)
			if _, err := msgStmt.Exec(sid, m, role, "2026-07-01T10:00:00Z", content); err != nil {
				b.Fatalf("insert message: %v", err)
			}
			if role == "assistant" {
				if _, err := usageStmt.Exec(msgID, rng.Intn(50000), rng.Intn(4000), rng.Intn(200000), rng.Intn(20000)); err != nil {
					b.Fatalf("insert usage: %v", err)
				}
			}
			if _, err := ftsStmt.Exec(msgID, content); err != nil {
				b.Fatalf("insert fts: %v", err)
			}
		}
		msgStmt.Close()
		usageStmt.Close()
		ftsStmt.Close()
		if err := tx.Commit(); err != nil {
			b.Fatalf("commit: %v", err)
		}
	}
}

func BenchmarkIngest(b *testing.B) {
	for _, spec := range drivers {
		b.Run(spec.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				db := openBench(b, spec)
				b.StartTimer()
				ingest(b, db)
			}
			b.ReportMetric(float64(sessionCount*messagesPerSession*b.N)/b.Elapsed().Seconds(), "msgs/s")
		})
	}
}

func BenchmarkFTSQuery(b *testing.B) {
	for _, spec := range drivers {
		b.Run(spec.name, func(b *testing.B) {
			db := openBench(b, spec)
			ingest(b, db)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for q := 0; q < ftsQueryIterations; q++ {
					term := vocabulary[q%len(vocabulary)]
					rows, err := db.Query(
						`SELECT rowid, snippet(msg_fts, 0, '[', ']', '…', 8) FROM msg_fts WHERE msg_fts MATCH ? ORDER BY rank LIMIT 20`,
						term,
					)
					if err != nil {
						b.Fatalf("fts query: %v", err)
					}
					n := 0
					for rows.Next() {
						var rowid int64
						var snip string
						if err := rows.Scan(&rowid, &snip); err != nil {
							b.Fatalf("scan: %v", err)
						}
						n++
					}
					rows.Close()
					if n == 0 {
						b.Fatalf("no FTS hits for %q — corpus broken", term)
					}
				}
			}
		})
	}
}

func BenchmarkAggregates(b *testing.B) {
	for _, spec := range drivers {
		b.Run(spec.name, func(b *testing.B) {
			db := openBench(b, spec)
			ingest(b, db)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for q := 0; q < aggrQueryIterations; q++ {
					rows, err := db.Query(`
						SELECT s.created_at, SUM(u.input_tokens), SUM(u.output_tokens),
						       SUM(u.cache_read_tokens), SUM(u.cache_write_tokens)
						FROM message_usage u
						JOIN messages m ON m.id = u.message_id
						JOIN sessions s ON s.id = m.session_id
						GROUP BY s.created_at
						ORDER BY s.created_at`)
					if err != nil {
						b.Fatalf("aggregate query: %v", err)
					}
					n := 0
					for rows.Next() {
						var day string
						var in, out, cr, cw int64
						if err := rows.Scan(&day, &in, &out, &cr, &cw); err != nil {
							b.Fatalf("scan: %v", err)
						}
						n++
					}
					rows.Close()
					if n == 0 {
						b.Fatal("aggregate returned no rows")
					}
				}
			}
		})
	}
}
