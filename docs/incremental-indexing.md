# Incremental Indexing

## Summary

Previously, ccpeek rebuilt the entire SQLite database from scratch on every
startup and every watch-mode tick. This was wasteful for large `~/.claude`
directories and meant data from deleted source files was silently lost.

This change introduces true per-source incremental indexing with source
fingerprinting, data retention for deleted sources, and a proper schema
migration system.

## Changes

### Schema migration system (`internal/store/schema.go`, `store.go`)

- Replaced the `CREATE TABLE IF NOT EXISTS` + version check approach with
  sequential migration functions
- `initialSchema` contains the baseline schema (equivalent to v4)
- `migrations` slice holds functions that run `ALTER TABLE` statements
- Migration v4→v5 adds `source_path` and `content_hash` columns to all entity
  tables, and updates `source_files` to use `content_hash` instead of
  `mtime_ns`
- `migrate()` reads current version from `meta`, applies pending migrations
  sequentially, each in its own transaction

### Per-file incremental indexing (`internal/index/index.go`, `incremental.go`)

- Each source path is fingerprinted and compared against the stored fingerprint
- Only files with changed content are re-indexed (old data deleted, new data
  inserted)
- Data from source files that have been deleted from disk is preserved by
  default
- FTS index is rebuilt from scratch when any session files change
- Directories (task groups, file history conversations) are hashed by combining
  all child file names and contents
- Cursor SQLite `state.vscdb` sources use metadata fingerprinting (`size + mtime`)
  to avoid full-file hashing on each incremental cycle

### Source path tracking (`internal/store/write.go`)

- All `Insert*` methods now accept a `sourcePath` parameter stored in each row
- `DeleteBySource` / `DeleteChildrenBySource` delete rows by source path
- `DeleteSessionCascade` handles the complex session deletion (messages, FTS,
  unlinking todos/file-history/task-groups/usage-facets)
- `UpsertProject` for incremental mode (projects may already exist)
- `PruneOrphanedProjects` removes projects with no remaining sessions
- `RebuildFTS` / `RepopulateFTS` for full FTS reconstruction

### CLI flags (`internal/cmd/root.go`)

| Flag        | Behavior                                                                                        |
| ----------- | ----------------------------------------------------------------------------------------------- |
| (default)   | Incremental: hash-compare per file, re-index only changed files, keep data from deleted sources |
| `--rebuild` | Drop all data and re-index from scratch (previous default behavior)                             |
| `--prune`   | Remove DB rows whose source files no longer exist on disk                                       |

### Watch mode

Watch mode (`--watch`) continues to use `RunIncremental()`, which now does true
per-file comparison instead of the old "any change → full rebuild" approach.

## Files changed

| File                                 | What changed                                                                  |
| ------------------------------------ | ----------------------------------------------------------------------------- |
| `internal/store/schema.go`           | Sequential migration system, v4→v5 migration                                  |
| `internal/store/store.go`            | `migrate()` rewritten for sequential migrations                               |
| `internal/store/write.go`            | `sourcePath` param on all Insert methods, delete/cascade helpers, FTS rebuild |
| `internal/store/read.go`             | `GetSourceFileHash`, `ListSourceFilePaths` (replaced mtime methods)           |
| `internal/index/index.go`            | `Run(rebuild bool)`, `RunIncremental()`, `Prune()`, hash helpers              |
| `internal/index/incremental.go`      | Filtered variants of all sub-indexers                                         |
| `internal/index/*.go`                | All sub-indexers pass `sourcePath` through                                    |
| `internal/cmd/root.go`               | `--rebuild` and `--prune` flags                                               |
| `internal/index/incremental_test.go` | Tests for all incremental behaviors                                           |
