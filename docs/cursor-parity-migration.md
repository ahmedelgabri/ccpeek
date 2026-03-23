# Cursor Parity, Migration, and Operations

This document defines what Cursor support can match with Claude support today,
where behavior is intentionally degraded, and what operators should expect in
production.

## Parity Matrix

| Area          | Claude         | Cursor JSONL                                  | Cursor SQLite                                  |
| ------------- | -------------- | --------------------------------------------- | ---------------------------------------------- |
| Conversations | Full           | Full                                          | Partial (`metadata-only` for some sessions)    |
| Commands      | Full           | Best effort from tool payloads                | Limited (depends on stored transcript details) |
| Plans         | Full           | Full (`*.plan.md`)                            | N/A                                            |
| Todos         | Full           | Full (plan frontmatter todos)                 | N/A                                            |
| File History  | Full           | Best effort from transcript tool calls        | Partial/inconsistent on older stores           |
| Snapshots     | Shell captures | Workspace git snapshots (different semantics) | N/A                                            |
| Tasks         | Full           | No native equivalent                          | No native equivalent                           |
| Paste Cache   | Full           | No native equivalent                          | No native equivalent                           |
| Usage Data    | Full           | No native equivalent                          | No native equivalent                           |
| Memories      | Full           | No native equivalent                          | No native equivalent                           |

## Degraded States

- Sessions indexed from Cursor SQLite may be stored as `metadata-only` when full
  transcript reconstruction is unavailable or unreliable.
- Metadata-only sessions:
  - render with explicit UI messaging,
  - can be navigated and filtered,
  - are excluded from transcript-body search,
  - export a metadata summary instead of message content.

## Migration Guidance

When upgrading from older ccpeek branches or custom Cursor integrations:

1. Upgrade to the new binary.
2. Run a full rebuild:
   - `ccpeek --rebuild`
3. Optionally prune stale source entries:
   - `ccpeek --prune`
4. Verify indexing output includes Cursor phases (projects/plans/sqlite/snapshots).
5. For large Cursor stores, choose an operational mode explicitly:
   - Fast mode (no Cursor SQLite): `ccpeek --cursor-sqlite=false`
   - Bounded SQLite mode: `ccpeek --cursor-sqlite-max-db-size-mb <N>`

Notes:

- Database schema migrations add source-aware and Cursor metadata columns.
- Source hashing and pruning now track Cursor JSONL, Cursor SQLite DB files, and
  snapshot repositories.
- Cursor app directory discovery checks:
  1. `--cursor-dir`
  2. `--cursor-dir/Cursor`
  3. platform default Cursor app dir (fallback only when `--cursor-dir` is `~/.cursor`)

## Performance and Scale Notes

- Cursor SQLite indexing can be expensive in environments with many
  `workspaceStorage` databases.
- Incremental indexing fingerprints Cursor SQLite DB files by metadata
  (`size + mtime`) instead of full content hashing.
- Disable Cursor SQLite indexing entirely when you only need transcript/plans/snapshot
  coverage:
  - `--cursor-sqlite=false`
- Apply a size bound to skip oversized DB files:
  - `--cursor-sqlite-max-db-size-mb 2048`
- Primary CI/e2e lane keeps Cursor disabled for deterministic speed:
  - `--cursor-dir ""`
- A dedicated fixture-based mixed-source e2e lane should validate Cursor UI behavior
  with a tiny deterministic dataset.
- For local operations, prefer incremental indexing (default) and only use
  `--rebuild` when necessary.

## Troubleshooting

### Cursor SQLite phases are slow

- Start with `--cursor-sqlite=false` to verify JSONL/plans/snapshots behavior first.
- Re-enable SQLite with a DB size limit for gradual rollout:
  - `--cursor-sqlite-max-db-size-mb <N>`
- Run with `--watch` only after a successful baseline index.

### Cursor SQLite projects are missing unexpectedly

- Confirm `--cursor-sqlite` is enabled.
- Confirm DB size does not exceed `--cursor-sqlite-max-db-size-mb`.
- Confirm your `--cursor-dir` points to the expected Cursor root.

### Metadata-only session confusion

- Metadata-only means transcript body reconstruction was unavailable for that source.
- These sessions are intentionally excluded from transcript-body search and full export.

## Operator Expectations

- Expect mixed-source pages (`projects`, `plans`, `todos`, `file-history`,
  `shell-snapshots`, `search`) to show source badges.
- Expect Claude-only sections (`tasks`, `paste-cache`, `usage-data`, `memories`)
  to remain empty if only Cursor data is present.
- Secret scan coverage is bounded by materialized content. Metadata-only sessions
  cannot be scanned for absent message bodies.
