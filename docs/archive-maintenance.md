# Archive maintenance in v2.1

CCPeek's database is an archive, not a disposable cache. It may hold the only remaining copy of a deleted transcript or imported v1 session. v2.1 keeps the existing CLI, `ccpeek/v1` response envelope and database location. Internal schema migrations 16 through 18 run in place. A v3 database or API is not required.

## Choose an archive explicitly

The default archive is `$XDG_DATA_HOME/ccpeek/ccpeek2.db`, or `~/.local/share/ccpeek/ccpeek2.db` without XDG configuration.

```sh
ccpeek --index-file /path/to/archive.db
ccpeek --index-file /path/to/archive.db query sessions --no-index
```

`--index-file` names the actual archive. Used alone, it does not import an unrelated default v1 database. `--data-file` remains the legacy import path and compatibility option. It derives `ccpeek2.db` from `ccpeek.db`, or `x.v2.db` from `x.db`. Supplying both flags selects an explicit archive and an explicit legacy import source.

## Back up before maintenance

```sh
ccpeek backup /safe/location/ccpeek-backup.db
ccpeek --index-file /path/to/archive.db backup /safe/location/another-backup.db
```

A backup includes committed WAL data, retained transcripts, usage claims, scan findings and ignore decisions. CCPeek creates a standalone SQLite snapshot with `VACUUM INTO`, checks integrity and foreign keys, syncs the file, and publishes it without overwriting an existing destination. Copying only a live `.db` file can miss committed WAL transactions. Use the backup command instead.

Backups contain plaintext conversation history and may contain credentials. CCPeek creates private files on Unix. Protect the destination and its backups accordingly.

```sh
ccpeek restore /safe/location/ccpeek-backup.db --index-file /path/to/restored.db
ccpeek --index-file /path/to/restored.db --skip-index --skip-scan
```

Restore requires a new archive path. It refuses an existing database or SQLite sidecars. It never replaces a file underneath a running server. Restoring under a new name also leaves the current archive available for comparison.

## Rebuild without erasing history

```sh
ccpeek --rebuild --index-only
```

Rebuild creates `<index>.before-rebuild-<UTC timestamp>.db` next to the archive before changing parser fingerprints. If that backup fails, rebuild stops. These backups have no automatic expiry; remove old copies only after checking that you have a usable backup elsewhere.

Available sources are reparsed transactionally. Missing and inaccessible sources remain in the archive, as do v1-only sessions. A successfully parsed source replaces its own current records, including cleared history files, empty task groups and sessions removed from a native database. Failed parses keep the previous transaction's records. A partial parse does not treat unseen source members as proof of deletion.

`--prune` is separate and opt-in. It removes missing sources only under available roots for the same agent. Permission and other access errors are not evidence of deletion. Imported v1 sessions remain exempt. Use backup before pruning too.

## Recovery and concurrent processes

Each source transaction marks derived data dirty and advances an archive generation. Changed sessions are recorded durably. If indexing stops after source commits but before links, workspaces or usage rollups finish, the next pass repairs them even when no source files changed. `--skip-index` can repair pending derived data without reparsing sources.

Normal refreshes reprice affected usage days. Pricing changes and explicit rebuilds still trigger full regeneration. Workspace identities survive refreshes. Duplicate request observations have a separate usage ledger; deleting or reparsing the message that owned usage reassigns that claim to a surviving copy of the same agent, content and request.

Usage corrections require an increased adapter parser version. For each request observed in an available source, the newest parser version takes precedence, even when its corrected counts or reported cost are lower. Within a version, the richest observation still wins. The ledger retains the best interpretation from each prior version with its source provenance; legacy claims use version zero and unknown provenance rather than guessing from a reassigned message owner. Requests absent from a reparse keep their existing claims. Rebuild does not wipe this history or permit older parsers to overwrite newer interpretations.

Indexing, migrations, imports, scans and backups coordinate through an OS-backed `<index>.lock` file. Waiting is cancellable. Process exit releases the lock; the remaining file is not evidence of a held lock, and deleting it while a process runs can defeat coordination. Readers continue using SQLite WAL snapshots. Opening a current-schema WAL archive does not acquire the maintenance lock. Use a local filesystem with working SQLite and file-lock semantics.

The HTTP server and `--open` browser launch do not wait for database initialization or migrations. The UI shows an initialization banner while `/api/v1/ready` returns 503 with `status: "initializing"`; health and SSE remain available. Data endpoints return 503 until the archive opens. Initialization failures stay visible in the UI, with details in the terminal. Once the archive opens, pages can read existing data while the background index pass waits for maintenance or processes sources.

## Check freshness and scan coverage

```sh
ccpeek query archive-status --no-index
ccpeek ingest --latest
ccpeek scan --no-index
```

`archive-status` is also available at `/api/v1/archive-status` and as an MCP tool. It reports schema and archive generation, pending derived repairs, imported-session counts, the last index outcome, and scan generation and rules fingerprint. HTTP health includes the same information. Readiness remains 503 after a partial index pass; the existing archive stays readable.

Secret scanning covers stored raw and canonical messages, tool arguments and commands, full stored tool results, and artifacts. Tool findings link to the issuing transcript turn. Rules and scan-algorithm changes invalidate old scan state. Automatic scanning catches up after `--skip-scan` and retries interrupted scans on later watch passes, even if indexing found no changes.

A completed scan is not proof that every original source was scanned. Unreadable or unsupported sources, parser omissions and truncated content are outside its coverage. Status reports warnings and truncations from the last index pass; those counts do not inventory all historical omissions. The scan page states this limit instead of presenting missing scan state as a clean result.

## Local preview privacy

Markdown images do not load automatically. Remote images become explicit links; following a link is a deliberate browser navigation outside CCPeek. The embedded UI's Content Security Policy also blocks external resource loads and connections.

Saved HTML reports are static previews. Active document elements are removed, scripts are disabled, and a response-level sandbox and resource policy apply even to direct navigation. Interactive report charts may no longer work. Original report content remains available through the artifact JSON API. Mutation requests with an Origin header must match the request's HTTP origin exactly; another localhost port is not the same origin.

## Tested storage formats

- Claude Code JSONL sessions and sidecar files, including cleared history and task directories.
- Pi's documented JSONL session format and fork relationships.
- Codex rollout JSONL, including `exec_command` with `cmd`, `shell_command` with a command string, and older command argument arrays.
- OpenCode legacy session/message JSON with separate `storage/part/<message-id>` directories, plus native SQLite session/message/part tables and WAL updates. SQLite sessions take precedence over leftover JSON copies. Unknown message and part fields remain in stored raw content.
- Cursor remains experimental. Its fixture-derived `store.db` support does not establish coverage of every Cursor version, and it does not extract tool calls.

Regression tests use generated or checked-in fixtures. They do not ingest the developer's personal agent history. Format support is not a guarantee that undocumented future schemas will parse.
