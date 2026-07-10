# ADR-0001: Adopt modernc.org/sqlite (pure Go) for the v2 store

Status: accepted · Date: 2026-07-10 · Phase: P0 (see `docs/v2-plan.md` §4)

## Context

v1 uses `mattn/go-sqlite3`, which requires CGO and the `sqlite_fts5` build
tag. This complicates the release matrix (cross-gcc for linux/arm64), blocks
plain `go install`, and is a recurring build foot-gun. The v2 plan proposed
switching to `modernc.org/sqlite` (pure Go, FTS5 compiled in) gated on a
benchmark of ccpeek-shaped workloads.

## Benchmark

`internal/db/driverbench` — bulk ingest (100 sessions × 120 messages with
usage rows + FTS5 indexing, one transaction per session), FTS5 MATCH queries
with `snippet()`, and dashboard-style aggregate joins. Run on linux/amd64
(Xeon 2.80GHz), Go 1.25.8, modernc.org/sqlite v1.53.0, mattn/go-sqlite3
v1.14.34:

| Workload | mattn (CGO) | modernc (pure Go) | Ratio |
|---|---|---|---|
| Ingest | 526 ms/op (22.8k msgs/s) | 732 ms/op (16.4k msgs/s) | 1.4× slower |
| FTS5 MATCH ×50 (12k docs) | 494 ms/op (~10 ms/query) | 1322 ms/op (~26 ms/query) | 2.7× slower |
| Usage aggregates ×50 | 179 ms/op (~3.6 ms/query) | 460 ms/op (~9.2 ms/query) | 2.6× slower |

Reproduce:

```sh
CGO_ENABLED=1 go test -tags sqlite_fts5 -bench . -benchtime 3x -run '^$' ./internal/db/driverbench
```

## Decision

Adopt **modernc.org/sqlite** for the v2 store.

- 16.4k msgs/s ingest clears the plan's 1 GB < 60 s target with headroom once
  multi-row batching lands; ingest is dominated by parsing, not the driver.
- ~26 ms FTS queries and ~9 ms aggregates are comfortably interactive for a
  localhost single-user app, and dashboards read from `rollup_usage_daily`,
  not live aggregates.
- FTS5 and `snippet()` work out of the box — no build tag.
- The structural win is distribution: `CGO_ENABLED=0` cross-compilation for
  all four release targets, no cross-gcc, working `go install`.

## Consequences

- v2 packages (`internal/db`, …) use driver name `sqlite` with
  `_pragma=`-style DSNs; v1 packages stay on mattn until deleted at parity.
- Both drivers coexist in `go.mod` during the transition.
- Fallback: v2 code speaks `database/sql` only, so swapping back to a CGO
  driver is a DSN/import change, not a rewrite. Revisit only if real-world
  histories (multi-GB, ≥100k-doc FTS) miss the §5.6 performance targets.
