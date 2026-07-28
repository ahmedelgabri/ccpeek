# CCPeek v2 — Rewrite Plan

Status: **v2.0 complete + post-launch UI/perf sprints landed** · Date: 2026-07-12

## Implementation status (updated as work lands on this branch)

Done — engine and agent surface:

- ✅ P0 complete: driver benchmark → modernc.org/sqlite adopted
  (ADR-0001); canonical session-centric model (`internal/canon`); adapter
  framework with env-aware root discovery (`internal/agent`);
  session-centric store, schema v1 with derived/user-state separation
  (`internal/db`); pricing with embedded LiteLLM snapshot
  (`internal/pricing`); fixture corpus (`testdata/agents/`).
- ✅ P1 complete: Claude Code adapter (sessions with real usage capture +
  all 10 sidecar sources), Pi adapter (documented format, tree, reported
  cost), agent-agnostic ingest pipeline (incremental hashing, per-source
  transactions, pending-link resolution, workspace facet, run telemetry),
  cost rollups (auto mode, unpriced visibility), typed query layer
  (sessions/session/transcript/usage/search), `ccpeek query` CLI with
  versioned JSON + exit codes, `/api/v1`, v1→v2 migration importer with
  zero-step first-run auto-migration.
- ✅ P3 launch set complete: Codex (cumulative token deltas), OpenCode
  (native tokens+cost), Cursor (SQLite-per-session via SourceDatabase).
  All five agents registered in the engine.
- ✅ MCP server (`ccpeek mcp`, dependency-free stdio JSON-RPC) and
  `ccpeek docs --agents` cheatsheet (§5.7 self-description).
- ✅ Live mode: fsnotify watch + `/api/v1/events` SSE + SPA cache
  invalidation (§5.5).
- ✅ SPA (Vite + React + TanStack + Tailwind, embedded via go:embed):
  sessions stream with filters and pagination, session detail (cost
  tiles, relations, artifacts, transcript with sidechains + tree view),
  usage explorer (groups, 5h blocks), search, unified
  artifact browser with server-rendered markdown, scan page with ignore
  toggles, session compare, ⌘K palette.
- ✅ Secret-scan ported to the v2 engine (all agents' transcripts and
  artifacts; ignore flags live in user_annotations natural keys).
- ✅ **v2.0 cutover complete**: the SPA serves at `/`, `/api/v1` is the
  only API, and every v1 route 301-redirects to its session-centric
  equivalent (`/projects/{dir}/{id}` → `/sessions/claude-code/{id}`,
  sidecar browsers → `/artifacts`, `/commands/` → `/search`, `/v2/*` →
  `/*`). `ccpeek scan`, `ccpeek export commands`, and `ccpeek ingest`
  run on the v2 store (`--claude-dir`/`--rebuild`/`--prune`/`--watch`
  keep working); Playwright specs cover the SPA at `/` plus the redirect
  map; README/docs refreshed.
- ✅ Serve-first startup (field feedback from the first large-corpus
  run): the port binds before indexing, the bootstrap runs in the
  background with throttled stderr progress and an in-UI banner, and
  `/api/v1/ready` gates scripts/tests on first data. source_files
  carries a size+mtime `stat_sig` so warm starts skip unchanged files
  without re-reading multi-GB histories (content hash remains the
  source of truth).

- ✅ Post-cutover janitorial: the v1 packages are deleted
  (`internal/store`, `internal/index`, `internal/server`,
  `internal/scan`, `internal/web`, the driver benchmark;
  `internal/model` shrank to the shell-history export formats), which
  dropped mattn/go-sqlite3, sqlx, and chroma — every build is now
  CGO_ENABLED=0 with no build tags (justfile, CI, release
  cross-compiles without gcc). The Nix package and the release archives
  build and embed the SPA (a `ccpeek-ui` derivation via pnpm.fetchDeps;
  binaries built without it served an empty UI).

- ✅ Follow-ups closed: the upgrade path is covered at the CLI wiring
  (a cmd test seeds a v1 db and asserts the first-run import + its
  idempotence, running in CI with every push); `ccpeek skill install`
  drops the agent skill into `~/.claude/skills` (or `--dir`); and the
  Usage page gained the ECharts cost explorer — daily spend stacked by
  agent with wheel/slider zoom, CSV export, and the rollup table as the
  accessible view (echarts loads as a lazy chunk only on that page).

Post-launch sprints (feedback-driven, after v2.0):

- ✅ **Instrument-panel UI overhaul**: sidebar shell (IBM Plex Mono/Sans),
  Overview dashboard at `/` (stat tiles with sparklines, activity
  heatmap with hover tooltips, per-agent totals, workspace facet,
  recent-file-edits feed, tool-calls-by-kind bars), day-grouped
  sessions stream, global `/commands` browser with copy/session links
  and zsh/bash/fish exports (`/api/v1/commands?format=`), SVG favicon,
  skeleton loading states.
- ✅ **Session detail as a hub**: deep-linkable tabs (transcript /
  commands / tools / files / artifacts), markdown-rendered transcripts
  (server-side goldmark), three visual registers (user accent /
  assistant violet / meta dashed one-liners that expand to the full
  stored text), kind-colored tool chips that expand inline (line diffs
  for edits, addition diffs for writes, highlighted commands), token-mix
  bar, per-file change lists with diffs, syntax highlighting
  (highlight.js lazy chunk, palette-mapped theme).
- ✅ **Filters and drill-downs everywhere**: from→to date pickers +
  agent (+ model on Usage AND Sessions — `SessionsFilter.Model` flows
  through CLI/API/MCP) on every data view; sortable columns on the
  usage + blocks tables; usage rows pivot into filtered sessions (day /
  agent / project); search hits open transcripts at the matching
  message (`?seq=`).
- ✅ **Cost source split**: rollups carry
  cost_reported_usd / cost_estimated_usd (agent-reported vs priced
  tokens — the API-vs-subscription proxy); split bars with tooltips in
  the usage table; rollups self-heal when empty; non-day groupings
  render an ECharts bar chart (tail-truncated path labels, agent
  colors for agent categories).
- ✅ **Perf/correctness from field reports**: a `query_only` read pool
  (4 conns) alongside the single writer so API queries never queue
  behind watch-mode ingest/rollups/scan (regression-tested); the secret
  scanner pages its reads; `/api/v1/usage` distinguishes 400 vs 500 via
  `query.ErrBadRequest`; ECharts instances re-create on stale DOM +
  resize on data-shape changes; the usage-report artifact renders in a
  sandboxed iframe via `/artifacts/.../raw`.
- ✅ **Incremental secret scan** (schema v5): `scan_state` remembers the
  content hash each session/artifact carried when last scanned, so a
  pass re-runs gitleaks only over entities whose hash moved, swaps just
  their findings in per-entity transactions, and drops state+findings
  for entities the index no longer holds. Detection fans out across
  GOMAXPROCS workers (the gitleaks detector is goroutine-safe).
  `ccpeek scan --full` discards the state for ruleset upgrades.
  Measured on a 947-session corpus: full sweep 51s (was ~10min
  single-threaded), nothing changed 38ms, one changed 3.6k-message
  session 8.2s.
- ✅ **Append-cursor ingest** (schema v6): `source_files.parse_state`
  stores a cursor (byte offset + prefix hash + sequence counters);
  adapters implementing `agent.TailParser` (Claude Code) verify the
  prefix and decode only appended bytes. The sink advances the session
  row in place (title/created_at survive) and inserts only new records;
  `tool_calls.external_id` + `canon.ToolResult` attach results that land
  after their issuing call was indexed. Rewritten/truncated sources fall
  back to a full parse that records a fresh cursor; partial trailing
  lines wait for the next pass. Measured on the active 2.6k-message
  session: 169ms vs 46.7s for the whole-file re-parse.

- ✅ **External review response (2026-07-13, batches 1–4)**: history
  re-ingest made idempotent (source-scoped replace); overview scan tile
  counts only active findings; the 52-week heatmap gets 371 days and
  UTC math; e2e exercises all five agents and oxlint covers the SPA;
  palette navigation fixed. Codex reasoning tokens documented as a
  SUBSET of output (fixture corrected — no cost change needed); usage
  session counts recomputed as true distinct sessions; sessions and
  commands page by offset; usage requests the full group range and the
  Blocks tab wires its agent filter (unsupported controls hidden). Scan
  finding identity is agent-qualified ("message/<agent>/<session>",
  "artifact/<agent>/<kind>/<name>") with wildcard rule-scoped ignores,
  and the v1 importer translates old "<session>@<timestamp>" ignore
  identities to v2 keys (proven by an import→scan→ignored test); the
  raw artifact endpoint carries CSP sandbox + nosniff. Artifact link
  resolvers match missing pairs (N:M), watch passes rescan incrementally
  and honor --prune, and the Nix toolchain pins Go 1.25.12
  (govulncheck-clean) with the pnpm fetcher on v3.

- ✅ **Release-gate fixes + deferred optimizations (2026-07-13+)**: the
  v1 importer now rescues EVERY retained entity class (todos, task
  groups, file history, usage facets/report, memories, prompt history,
  and tool calls for orphan sessions — verified on a real 1.2GB v1 db:
  1,606 sessions, 149k messages, 29.6k tool calls, 867 artifacts in
  8.8s), skipping only what v2 actually holds; Pi emits tool calls from
  its real toolCall/toolResult format (20k calls extracted from a live
  corpus, 99.98% result-paired); Cursor's meta selection is
  deterministic and its fixture-based capability level is labeled
  honestly (no real store.db exists to spike against); binaries built
  without the SPA say so instead of serving a blank page. Optimizations:
  the transcript is virtualized (TanStack Virtual — 21–33 mounted rows
  at any depth of a 4.3k-message session), session-list costs aggregate
  in one grouped query, append passes read the source once (the running
  SHA-256 hands off from change detection to the tail parser), and
  internal/ops defines every read operation once with the CLI and MCP
  generated from it (ten ops each, matching HTTP; the once-missing
  model/full filters everywhere).

- ✅ **Second review round: the two upgrade-boundary CRITICALs
  (2026-07-22)**: the v1 scan-ignore importer now maps all ELEVEN v1
  source types explicitly — paste_cache→paste, todo→todo_list
  (`#item-N` stripped), task→task_group (`#task-*` stripped),
  usage_report `report`→`report.html`, command collapsing into its
  containing message, the rest passing through by name — each proven by
  an import→full-scan reattachment test, with the memory
  empty-file-name edge pinned as a deliberate orphan annotation and
  per-item ignores deliberately coarsening to whole-artifact rule
  wildcards. The import is version-adaptive (sqlite_master + PRAGMA
  table_info drive every SELECT): pre-v13 databases no longer fail on
  columns they never had, table-absent is distinguished from
  column-absent (which previously skipped whole tables silently), and
  the commands table of pre-tool_calls vintages (v7/v10) imports as
  shell tool calls instead of vanishing. The historical fixture corpus
  (v4–v14) is restored to internal/migrate/testdata/v1 with per-vintage
  import + idempotency tests. The import outcome is tracked apart from
  migrated_at: tri-state v1_import_state (success/failed/no-legacy-db)
  with v1_imported_at stamped only on success, failures retained in
  v1_import_error, surfaced via /api/v1/health and a UI banner, retried
  on every start (already-stamped WIP databases get one idempotent
  re-import), and `ccpeek migrate` exiting non-zero.

- ✅ **Re-review closure (2026-07-22)**: pre-v15 memories tables (no
  file_name column — that column IS v1's final migration) import under
  the same 'MEMORY.md' default the v14→v15 migration backfilled, with
  the v14 fixture carrying the real vintage shape; /api/v1/ready holds
  at 503 "v1-import-failed" while v1_import_state=failed (partial
  history must not read as ready — health stays 200 with the detail);
  and legacy-file stat errors other than not-exist record failed (and
  retry) instead of the permanent no-legacy-db.

- ✅ **Remaining review groups 3–7 (2026-07-22)**: accounting — the
  canon.Usage contract makes OutputTokens the billable output
  (reasoning included); OpenCode's additive reasoning folds in at the
  adapter while Codex's subset semantics pass through, so every surface
  sums one normalized column, and Blocks counts true per-window
  distinct sessions (cross-provider and disjoint-model tests pin
  both). Contracts — no silent caps: Artifacts pages (clamp removed),
  Usage defaults to ALL groups (aggregate surface), session tool calls
  page with everything-by-default, Compare picks via server-side title
  filter with truncation flagged, cap+1 tests on each; --data-file
  derives a per-name v2 store (a.db → a.v2.db) so profiles never
  alias. Scalability — MCP serves initialize immediately with a
  transport-owned status tool while indexing runs behind (reads see a
  visibly warming archive during the pass — per-source commits land
  incrementally, rollups regenerate at the end — not a frozen snapshot); all three
  JSONL adapters stream records (memory bounded by a line, not a
  session; schema v7 adds the result-pairing index) and skip oversized
  lines as diagnostics with the cursor spanning the skip. Cleanup —
  Cursor is labeled experimental in the README capability matrix and
  every UI agent selector; search filters agents server-side and
  artifact hits link to their artifact page; the plan/memory resolvers
  reconcile stale content_ref links transactionally via hash joins;
  the bootstrap scan completes before watch starts (ingest and
  scanning never overlap) and rollups regenerate only on passes that
  changed data (the regeneration itself is a full rebuild — see
  RegenerateRollups' scope note); the ops registry covers every HTTP domain read (tools,
  artifact added — CLI/MCP gain them by generation) with a
  classified route table enforcing transport parity; and the
  failed→no-legacy-db transition clears the stale import error.

- ✅ **Release polish round (2026-07-23)**: backward transcript pages
  tile to the anchored gap ({from, limit} page params + seq dedupe; a
  1300-message e2e fixture pins from=0&limit=400 after a ?seq=500 deep
  link). Tool transfer is lazy end to end: list rows carry no diff
  excerpts, the transcript requests compact range-scoped chips only,
  tabs start their paged fetch on first open, and expansion fetches
  one call's detail (`tool` op / /tools/{seq}); e2e proves no eager
  loop and no excerpt before expansion. The MCP status wording says
  warming archive, not "last complete archive". The `withui` build tag
  makes the full product's UI a COMPILE-time guarantee across just/
  Nix/release paths while plain `go build`/`go install` is the
  explicit API-only variant. HTTP contracts are normalized (empty
  lists as [], centralized typed parameter parsing, 400 on malformed
  integers/dates and negative offsets, contract tests across every
  list surface). Dormant slices finished or parked: prompt history is
  queryable via a `history` op on all transports, the dead SQL pricing
  table is removed (schema v8) with runtime pricing refresh honestly
  parked, and
  `ccpeek doctor` prints resolved roots (with mechanism), database
  paths, and migration state read-only.

**Schema baseline decision:** the migration machinery stays parked until
this branch lands on main; at that merge the baseline freezes and every
later schema change ships as a migration. New agents (Gemini CLI,
Droid, Amp, …) land post-launch per §6 as demand shows up.

**Schema policy:** the store is an archive, not a cache — it retains
sessions whose source files were cleaned up (prune is opt-in),
v1-imported orphans, and user annotations, none of which a
rebuild-from-sources could restore. The migration machinery lives in
`internal/db` (`migrations` slice anchored at `baseVersion`,
transactional apply, version stamping) but stays dormant until the v2.0
release: pre-release schema changes edit the initial schema directly
and bump `schemaVersion`/`baseVersion` together, and a database stamped
older than the baseline refuses to open with re-create instructions —
never a silent wipe. From the release onward the baseline freezes and
every schema change ships as a migration entry; a rebuild is never an
acceptable upgrade path, and migrating in place keeps startup instant.
The initial schema is always the latest (fresh databases never replay
migrations), open performs no backfills, and a database from a newer
ccpeek refuses to open. The v1 `ccpeek.db` → v2 importer
(`internal/migrate`) is a separate, mandatory one-time migration.

---

This document is the full plan for CCPeek v2: a multi-agent, cost-aware rewrite
of the engine with a managed migration path from v1. It is based on a deep audit
of the current codebase (schema v15, ingest pipeline, web UI, CLI, CI/release)
and research into the storage formats of other coding agents.

---

## 1. Executive summary

**Recommendation: rewrite the engine, renovate the UI, keep the chassis.**

- **Stay in Go.** The pain points in v1 are architectural (Claude-only data
  model, derived-data/user-state conflation, startup backfills, no usage
  capture) — not language-level. Go keeps the killer distribution story
  (single binary via Homebrew/Nix/tarballs) and the mature release pipeline.
- **Switch to pure-Go SQLite** (`modernc.org/sqlite`) to drop CGO. This
  simplifies the 4-arch release matrix (no more cross-gcc), enables plain
  `go install`, and removes the `sqlite_fts5` build-tag foot-gun.
- **v2 core = canonical agent-neutral data model + adapter framework.**
  The launch set is five agents chosen to cover ~80% of coding-agent users:
  **Claude Code, Pi, Codex CLI, OpenCode, and Cursor**. Claude Code and Pi are
  built in Phase 1 — implementing the framework against two very different
  agents from day one is what proves the abstraction — with Codex, OpenCode,
  and Cursor completing the set in Phase 3. Gemini CLI, Droid, Amp, and others
  follow post-launch, driven by demand. Every entity gets an `agent`
  dimension, and the model is **session-centric**: the session is the hub
  every other entity relates to; directories/paths are session attributes and
  provenance, never hierarchy (§5.2) — the only model that fits all five
  launch agents (Codex stores by date, Cursor by workspace hash).
- **Real token + cost accounting** becomes a first-class subsystem: capture
  `message.usage` (input/output/cache-write/cache-read/reasoning tokens) and
  `model` per message, price via an embedded LiteLLM/models.dev snapshot,
  aggregate into rollups for dashboards.
- **Agent-friendly by design.** One typed query layer, three transports —
  `ccpeek query --json` CLI, localhost `/api/v1`, and an MCP server — so
  agents can ask "have I solved this before?", pull compact transcripts, or
  check spend, with token-efficient output and stable schemas (§5.7).
- **Rich UI as a priority: embedded SPA.** The web app becomes a
  Vite + React + TypeScript SPA served from `go:embed`, consuming the same
  `/api/v1` the agents use — full interactivity (virtualized transcripts,
  cross-filtered cost analytics, live-updating dashboards, tree views,
  command palette) with **zero change to the single-binary distribution**;
  Node stays a build-time-only dependency, as it already is for Tailwind.
  Astro (incl. v7) was evaluated and rejected for the app UI (§4).
- **Full schema redesign, with automatic migration on first run.** The v2
  schema is a clean break (§5.2) — no attempt to evolve v1's tables in place.
  This is safe because the DB is a derived index: the primary migration path
  is re-ingest from source, plus an import step for the two things that are
  _not_ re-derivable: rows whose source files were deleted (v1's retention
  feature) and user state (scan ignore flags). The entire flow runs
  **automatically on the first v2 start — zero manual steps**. v2 writes a
  new DB file; v1's DB is never touched, so rollback is "run the old binary."

Phases: **P0 foundations → P1 core engine + Claude & Pi adapters + cost +
migration → P2 cost/analytics UI → P3 more agent adapters → P4 live mode &
power features.** Each phase ships.

---

## 2. Current state (v1 audit)

### 2.1 What v1 does well — keep these

- **Incremental indexing** by SHA-256 content hash with retention of data from
  deleted sources (`internal/index/index.go`), plus `--rebuild` / `--prune`.
- **Ingest diagnostics** (`ingest_runs` / `ingest_issues`, `ccpeek ingest`) —
  per-run telemetry with per-issue detail. Rare and valuable; keep as-is.
- **Secret scanning** with gitleaks (150+ rules), redacted display, ignore
  toggles, exit-code-2 CI usability.
- **Migration fixture testing** — paired `.sql`/`.db` snapshots per schema
  version with a CI drift check (`scripts/check-migration-fixtures.sh`).
- **Release automation** — 4-arch builds, completions + man pages, Homebrew
  tap auto-update, Nix flake.
- **Local-first privacy** — nothing leaves the machine. Non-negotiable in v2.
- **Server-rendered UI with tiny JS** — fast, dependency-light, CSP-strict.
  It served v1 well, but v2 replaces it with an embedded SPA because UI
  richness is a stated v2 priority (§4). The properties worth preserving —
  speed, strict CSP, zero runtime dependencies, keyboard-first UX — carry
  over as hard requirements on the new stack rather than reasons to keep the
  old one.

### 2.2 Confirmed bugs and design debt

Data layer (`internal/store`, `internal/index`):

1. **No real usage capture.** The JSONL parser (`model.go` `RawJSONLLine` /
   `MessagePayload`) reads only `type, timestamp, uuid, sessionId, cwd,
gitBranch, message.{role,content}`. It silently discards `message.usage`
   (input/output/cache tokens), `message.model`, `costUSD`, `requestId`,
   `parentUuid`, and `isSidechain`. "Tokens" in the UI are `chars/4`
   (`projects.go:255`) — an estimate presented as a token metric, and it even
   excludes tool_use/tool_result text.
2. **Startup backfills scan everything on every open.** `Store.migrate`
   always runs `backfillToolCalls` + `backfillSearchIndex` even at current
   schema version (`store.go:123-128`) — two full `messages` table scans with
   JSON re-parsing on _every_ start.
3. **`initialSchema` drift.** The "v4 baseline" has drifted to ~v15 shape, yet
   fresh DBs still replay all 11 migrations, recreating core tables several
   times (`schema.go`).
4. **Content stored ~3×.** Message text lives in `messages.content` (raw
   JSON), `messages_fts`, and `search_documents_fts`.
5. **Dead schema.** The `commands` table is written but never read (all read
   paths use `tool_calls WHERE tool_kind='shell'`); `tool_calls.searchable_text`
   and `result_text` are write-only.
6. **Single giant transaction + unbounded memory.** Full index runs in one
   transaction; `readJSONL` materializes whole files; `indexProjects` holds all
   messages of all sessions of a project in RAM; a JSONL line >10 MB aborts the
   whole file (`jsonl.go`).
7. **Error swallowing.** JSON marshal failures collapse to `{}`; `LastInsertId`
   errors ignored; `hashDir` silently skips unreadable files, so corruption can
   go undetected by incremental runs.
8. **Missing indexes** on `messages.timestamp`, `sessions.created_at`, and
   others used by timeline/scan queries.
9. **User state doesn't survive `--rebuild`.** `Store.Reset` drops every table,
   including `scan_findings.ignored` — user decisions are lost.

Web layer (`internal/server`, `internal/web`):

10. **No pagination** on most list pages (plans, snapshots, todos, tasks,
    paste-cache, usage-data, memories, file-history, projects) — full dumps
    filtered client-side.
11. **No live updates** — `--watch` re-indexes server-side but the browser
    never learns; watch is a 30 s ticker, not fsnotify.
12. **Panic risk** in `conversationExport` (`role[:1]` on possibly-empty
    string, `handlers_conversation.go:336`); lossy export (tool results
    truncated to 500 chars).
13. **Session compare** loads every session in the project into memory and
    scans linearly for `?a`/`?b`.
14. **Accessibility** — charts/heatmap are hover-only divs/rects, no ARIA, no
    keyboard access; color-only status signals.

### 2.3 Claude-only assumptions that block multi-agent

- `~/.claude` layout hardcoded in `collectSourceFiles` and every indexer.
- Anthropic message envelope + content-block model baked into
  `MessagePayload`/`ContentBlock`.
- Claude's tool names hardcoded (`Bash`, `Read`, `Write`, `Edit`, `MultiEdit`,
  `Glob`, `Grep`, `Task`) in `normalizeToolKind` and JSON-path extraction.
- Claude's `-`/`--` project-dir encoding in `Encode/DecodeProjectDir`.
- **No agent/provider column anywhere** — sessions/messages can't say which
  tool produced them.
- Claude-only artifact types (plans, shell-snapshots, todos, tasks, usage
  facets, file-history, paste-cache, memories) enumerated as fixed strings in
  search groups and `SourceURL` routing.

---

## 3. Goals and non-goals for v2

**Goals**

1. Support multiple coding agents behind one canonical data model.
2. Accurate token accounting and cost estimation (per message → session →
   project → model → agent → day), including cache economics.
3. Be a first-class data source _for_ agents, not just a viewer of their
   output: every question the UI answers is also answerable via JSON CLI,
   local HTTP API, and MCP (§5.7).
4. Managed migration: no data loss, no user-state loss, trivial rollback.
5. Fix the structural debt (startup scans, duplication, memory, pagination).
6. Keep local-first privacy, single-binary distribution, and the existing
   quality bar (unit + e2e + migration-fixture CI).

**Non-goals (v2.0)**

- Cloud sync / multi-machine merge (design IDs so it's possible later).
- Team/server deployments, auth.
- Modifying or writing to any agent's data directory (strictly read-only).

---

## 4. Stack decision

| Option                     | Pros                                                                                                                                                                     | Cons                                                                                                                                                                               | Verdict                  |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------ |
| **Go (rewrite internals)** | Single static binary; existing release infra (Homebrew/Nix/4-arch) and test culture carry over; gitleaks is a Go library; maintainer fluency; pure-Go SQLite removes CGO | Weak native UI ecosystem — solved by pairing it with a TypeScript SPA over `/api/v1` (below), not by leaving Go                                                                    | **Recommended** (engine) |
| TypeScript (Bun/Node)      | Richest UI ecosystem; fastest analytics-UI iteration; ccusage precedent                                                                                                  | Distribution regresses (large `bun compile` binaries or runtime dependency); no gitleaks equivalent — secret scanning would be lost or shelled out; whole release pipeline rebuilt | No                       |
| Rust                       | Fast, single binary, no CGO                                                                                                                                              | Slowest velocity; total rewrite incl. scanning; perf bottleneck is SQLite/IO anyway                                                                                                | No                       |

Within Go, two concrete engine changes:

- **`modernc.org/sqlite`** (pure Go, FTS5 included) replaces
  `mattn/go-sqlite3` + CGO. Phase 0 includes a benchmark gate (ingest + FTS
  query perf on a large real `~/.claude`); fallback is staying on CGO with the
  rest of the plan unchanged.
- **Keep cobra, goldmark, chroma, gitleaks, difflib.** They earn their keep.

### 4.1 Web UI: embedded SPA (UI richness is a v2 priority)

UI richness is a stated priority for v2. The interactions that implies —
virtualized transcripts for huge sessions, cross-filtered cost analytics with
brush/zoom, live-updating dashboards, conversation-tree visualization, session
replay, a command palette — are past what server templates + vanilla JS can
carry. v2 therefore replaces `html/template` with a **single-page app served
from `go:embed`**:

- **Stack**: Vite + **React + TypeScript**; TanStack Router (type-safe routes
  preserving v1's URL scheme) + TanStack Query (data fetching, with SSE-driven
  cache invalidation for live updates everywhere); Tailwind v4 (already in the
  build); shadcn/ui on Radix primitives (accessible components — fixes v1's
  ARIA/keyboard gaps by construction); **ECharts** for analytics (brush/zoom,
  heatmaps, treemaps, timelines in one dependency — bundle size is a non-issue
  for a localhost app whose assets ship embedded anyway).
- **Distribution is unchanged.** `vite build` output is embedded via
  `go:embed`, exactly like today's CSS. Single binary; Node/pnpm remain
  build-time-only (they already are, for Tailwind). Nix/Homebrew/release
  pipelines keep working with a swapped build step.
- **The Go server slims** to engine + `/api/v1` + static assets. The SPA is
  `/api/v1`'s first and heaviest client — which keeps the agent-facing API
  (§5.7) complete and battle-tested by construction.
- **Server-side rendering of rich content stays in Go**: markdown (goldmark),
  code highlighting (chroma), and diffs (difflib) are returned as sanitized
  HTML fragments in API payloads — one hardened XSS path, reused by UI and
  agent transcript export alike.
- **Carried over as requirements**: strict CSP (all assets self-hosted),
  v1's keyboard-first UX (`j`/`k`/`/`, plus a proper ⌘K palette), URL
  compatibility (§8.2), Playwright e2e (port 4322 setup reused; specs ported).
- **Trade-off accepted**: the no-JS fallback goes away. On localhost, with
  code-splitting and prefetching, perceived speed is preserved.

**Considered and rejected — Astro (incl. v7).** Its SSR mode needs a JS
runtime at runtime, breaking single-binary distribution; static output cannot
serve a fully dynamic data app (every ccpeek page is a live SQLite query, and
watch mode/live tail invalidate constantly); and its islands model optimizes
for mostly-static content — the opposite of this app. A plain SPA over
`/api/v1` is the honest fit. Astro remains a great choice for a future
project docs/marketing site, which is exactly its sweet spot.

**Considered — Solid or Svelte** instead of React: lighter and faster, both
viable. React is recommended for ecosystem depth (TanStack, Radix/shadcn,
every charting library) and agent-assisted development velocity on a
solo-maintainer project. Final call is a P0 decision (§11); the `/api/v1`
contract contains the blast radius either way.

Naming: `ccpeek` stays the binary/brand ("**c**oding-**c**LI peek" once
multi-agent lands). A rename would burn Homebrew/Nix continuity for zero
functional gain. Revisit after Phase 3 if desired.

---

## 5. v2 architecture

### 5.1 Adapter framework

```go
// internal/agent
type Adapter interface {
    Slug() string                          // "claude-code", "pi", "codex", "opencode", "cursor"
    DetectRoots() []Root                   // default dirs + flag/env overrides
    Discover(root Root) ([]SourceRef, error) // files/dirs + content hashes
    Parse(src SourceRef, sink RecordSink) error // emit canonical records
    Watch(ctx context.Context, root Root, ch chan<- SourceRef) error // fsnotify
}

// RecordSink receives canonical records:
//   Session, SessionRelation, Message, Usage, ToolCall,
//   Artifact, ArtifactLink (evidence-carrying session link), HistoryEntry
```

- **Root discovery must respect each agent's own relocation mechanisms.**
  Most agents let users move their data directory; hardcoding platform
  defaults (v1's `~/.claude` mistake) silently indexes nothing for those
  users. `DetectRoots()` resolves in precedence order:
  1. explicit ccpeek config/flags (`--root claude-code=~/backup/claude`,
     repeatable — multiple roots per agent are supported, e.g. an archive
     copy next to the live directory);
  2. the agent's own environment overrides, honored exactly as the agent
     itself would: `CLAUDE_CONFIG_DIR` (Claude Code), `PI_CODING_AGENT_DIR`
     (Pi), `CODEX_HOME` (Codex), `OPENCODE_DATA_DIR` (OpenCode — note: a
     comma-separated _list_ of directories); Cursor's override, if any, is
     part of the P3 spike;
  3. platform defaults (`~/.claude`, `~/.pi/agent`, `~/.codex`,
     `~/.local/share/opencode`, `~/.cursor/chats`).
     Resolved roots are recorded per ingest run (in `ingest_runs`) so "why is my
     data missing" is diagnosable, and `ccpeek doctor` prints which roots were
     detected, from which mechanism.
- The **core pipeline** (hashing, incremental diff, transactions, diagnostics,
  FTS, secret scan) is agent-agnostic and lives once, in `internal/ingest`.
- Each adapter is pure translation: agent format → canonical records. This is
  the piece that varies per agent, and the only piece.
- v1's per-source indexers become the **Claude adapter**, emitting artifacts
  for plans/todos/tasks/snapshots/paste-cache/memories/file-history/usage
  facets.
- Tool normalization moves into adapters: each maps its native tool names to a
  shared `tool_kind` taxonomy (`shell | file_read | file_write | file_edit |
search | discovery | subagent | web | other`), while preserving the native
  name.

### 5.2 Canonical schema (v2 sketch) — session-centric

**The session is the hub of the model.** Everything else is defined by its
relationship _to a session_; directories and paths are session attributes and
ingest provenance, never identity or hierarchy. This isn't just taste — it's
what the launch agents force:

- **Directory-as-container doesn't generalize.** Codex organizes sessions by
  _date_ (`sessions/YYYY/MM/DD/`) with no project directory at all; Cursor
  groups by opaque workspace hash; Pi and Claude encode cwd lossily into a
  dir name (Claude's `-`/`--` mangling can't round-trip paths containing
  `-`). Only the session exists in every agent.
- **A session's directory isn't even stable.** Claude Code records `cwd` per
  message and it can change mid-session; sessions run outside any repo.
- **v1's weakest links come from path-thinking**: plans, snapshots, and
  paste-cache entries are indexed as loose files with _no_ session
  relationship at all; todos/tasks/file-history are linked by fragile
  filename conventions buried in indexers.

Design principles: (a) **session-centric** — every entity either belongs to a
session or is connected to sessions through an explicit, evidence-carrying
link table; "project" is a derived grouping facet over sessions' `cwd`, not a
container; (b) every entity carries `agent_id`; (c) **derived data and user
state live in disjoint table sets** — rebuild may drop the former, never the
latter; (d) natural keys (agent slug + external IDs) everywhere user state or
cross-references attach, so they survive re-ingest; (e) paths appear only as
provenance (`source_path`) and session attributes (`cwd`), never as join keys.

```sql
-- dimensions
agents(id, slug UNIQUE, display_name)
-- (a pricing table was planned here; pricing ships as an embedded
--  snapshot instead, and the stored form is parked until a runtime
--  refresh has a consumer)

-- THE HUB (derived, rebuildable from sources)
sessions(id, agent_id, external_id, title, created_at, modified_at,
         cwd, repo_root, git_branch,        -- context attributes, not hierarchy
         model_mix, origin DEFAULT 'ingest', -- 'ingest' | 'imported-v1' | 'archive'
         source_path, content_hash,          -- provenance only
         UNIQUE(agent_id, external_id))

-- session ↔ session relationships (the graph agents actually produce)
session_relations(from_session_id, to_session_id, kind, evidence_json,
                  PRIMARY KEY (from_session_id, to_session_id, kind))
  -- kinds: resumed_from, fork_of, sidechain_of (Claude Task subagents),
  --        compacted_into (Codex/Pi compaction lineage)

-- owned by exactly one session
messages(id, session_id FK, seq, external_id, parent_external_id, role, kind,
         created_at, model, cwd, content /* raw JSON */)
message_usage(message_id PK, input_tokens, output_tokens,
              cache_write_tokens, cache_read_tokens, reasoning_tokens,
              service_tier, reported_cost_usd, request_id)
tool_calls(id, session_id FK, message_id FK, seq, name, kind, input_json,
           result_status, result_excerpt, file_path, started_at)

-- artifacts stand alone, then RELATE to sessions (n:m, with evidence)
artifacts(id, agent_id, kind, name, content, metadata_json,
          source_path, content_hash, UNIQUE(agent_id, kind, name))
  -- kinds: plan, todo_list, task_group, shell_snapshot, paste, memory,
  --        file_history, usage_facet, usage_report, checkpoint, ...
  -- structured children stay relational: todo_items, task_items, file_versions
artifact_sessions(artifact_id, session_id, relation, evidence,
                  PRIMARY KEY (artifact_id, session_id, relation))
  -- relation: produced_by | applies_to
  -- evidence: id_match | filename_uuid | cwd_match | content_ref | manual
  -- the link resolver is a distinct ingest stage and can improve between
  -- releases without re-parsing sources — v1's unlinked plans/snapshots/
  -- pastes become linkable over time instead of forever orphaned

-- derived grouping facet (regenerated at ingest from sessions.cwd;
-- powers the "Projects" view but is NEVER a parent container)
workspaces(id, canonical_path UNIQUE, display_name)
session_workspaces(session_id, workspace_id, PRIMARY KEY (session_id, workspace_id))

history(id, agent_id, display, timestamp, session_id NULL)
source_files(path PK, agent_id, content_hash, indexed_at)   -- ingest bookkeeping
ingest_runs / ingest_issues                                  -- carried from v1
rollup_usage_daily(day, agent_id, workspace_id, model,
                   sessions, messages, input_tokens, output_tokens,
                   cache_write_tokens, cache_read_tokens, cost_usd)
search_fts   -- ONE fts5 table (text + type/url metadata); every hit resolves
             -- to a session (or an artifact with its session links)

-- user state (NEVER dropped by rebuild/reset), attached by natural key —
-- for session-scoped state that's (agent_slug, session external_id)
user_annotations(id, entity_type, natural_key, kind, value_json, created_at)
  -- kinds: scan_ignore, pin, note, tag, saved_search
scan_findings(..., ignored moves to user_annotations via natural key)
```

Schema hygiene rules (fixing v1's drift):

- `initialSchema` **is always the latest schema**; fresh DBs never replay
  migrations. Existing DBs run only the pending migration functions.
- No backfills at open time. Backfill-style work is an explicit migration with
  a version bump, run once.
- `Reset` drops derived tables only; user-state tables survive `--rebuild`.
- Keep the migration-fixture CI system; generate a fixture per v2 schema
  version from day one.

### 5.3 Cost & token engine

**Capture** (per assistant message, via adapters):

| Agent                    | Source of truth                                            | Shape                                                                                                                                                                                           |
| ------------------------ | ---------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Claude Code              | `message.usage` + `message.model` on assistant JSONL lines | `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `service_tier`; older versions also wrote `costUSD`                                                  |
| Pi                       | `usage` on assistant message entries                       | `input`, `output`, `cacheRead`, `cacheWrite`, `totalTokens` **plus a pre-computed `cost` breakdown** (`cost.input/output/cacheRead/cacheWrite/total`); model tracked via `model_change` entries |
| Codex CLI                | `token_count` events (cumulative)                          | subtract previous totals → per-turn input / cached input / output / reasoning; logs before 2025-09 have none                                                                                    |
| OpenCode                 | per-message JSON                                           | token + cost fields present                                                                                                                                                                     |
| Cursor CLI               | hex-encoded JSON blobs in per-session `store.db`           | usage metadata incl. input/output/cache tokens and cost — confirm exact field names in the P3 spike                                                                                             |
| Gemini CLI (post-launch) | session checkpoint JSON                                    | per-turn token stats incl. cached                                                                                                                                                               |

**Correctness details that v1-style naive parsing would get wrong:**

- **Dedupe usage** by `(message external_id, request_id)` — resumed/forked
  Claude sessions duplicate assistant lines across JSONL files; without dedupe,
  costs double-count. (This is the same approach ccusage uses.)
- **Cumulative counters** (Codex) need delta derivation with reset detection.
- **Unknown models must be visible**: usage rows that can't be priced are
  aggregated as "unpriced tokens," never silently $0.

**Pricing:**

- Embed a snapshot of LiteLLM's `model_prices_and_context_window.json`
  (cross-provider, includes cache-write/read rates) at build time. The
  embedded snapshot is the ONLY runtime pricing source; a runtime
  refresh (`ccpeek pricing update` into a SQL pricing table) is PARKED
  until it has a consumer — the dead table was removed from the schema
  so the stored shape cannot drift from reality. Fully
  offline-capable.
- Model-key normalization layer (`claude-sonnet-5` ≡ `anthropic/claude-sonnet-5`
  ≡ Bedrock/Vertex ids).
- Cost is **computed from tokens** against the embedded pricing
  snapshot, materialized into `rollup_usage_daily` for dashboard speed;
  rollups regenerate when session data changes. Where the agent
  reported its own cost (Pi's `cost.total`, legacy `costUSD`, OpenCode), store
  it and offer ccusage-style modes: `auto` (prefer reported) / `calculate` /
  `display`.
- **Honest labeling:** for subscription users (Pro/Max) dollar figures are
  "estimated API-equivalent value," not billing. The UI says so.

### 5.4 Search

- One FTS5 index (`search_fts`) replacing `messages_fts` +
  `search_documents_fts`. Filters: agent, workspace, type, model, date range,
  tool name. Snippets + ranking as today.
- Optional later (P4): local semantic search over session summaries
  (sqlite-vec + a small local embedder), strictly opt-in.

### 5.5 Live mode

- Replace the 30 s ticker with **fsnotify** watchers per adapter root
  (debounced), falling back to polling where fsnotify is unreliable.
- **SSE endpoint** (`/events`) feeds TanStack Query cache invalidation in the
  SPA — every open view updates live (lists, dashboard tiles, transcripts),
  not just a dedicated tail page.
- Headline feature: **live session tail** — open a running session and watch
  messages/tool calls stream in. This is the "peek" the name promises.

### 5.6 Performance targets (gate in CI where practical)

- Warm start (schema current): **< 200 ms** to listening (no backfills).
- Ingest: per-source-file transactions with batched multi-row inserts;
  1 GB of JSONL **< 60 s**; bounded memory (stream sessions, don't hold a
  project's full message set).
- Dashboard queries **< 50 ms** from rollups; every list paginated server-side.

### 5.7 Agent-facing query surface (one query layer, three transports)

v2 is not just a viewer _of_ agent data — it is a data source _for_ agents.
A single typed query service (`internal/query`) backs every surface, so
anything the UI can show, an agent can fetch:

- **CLI**: `ccpeek query <op> --json` (NDJSON for streams). Reads the SQLite
  DB directly — **no server required** — because shell-oriented harnesses
  (Claude Code's Bash tool, Pi, scripts, CI) reach for a command before
  anything else.
- **HTTP API**: versioned JSON under `/api/v1/*` on the existing localhost
  server, mirroring the CLI ops 1:1. The v2 SPA (§4.1) is this API's first
  client, so the agent-facing surface stays complete and battle-tested by
  construction — if the UI can show it, an agent can query it.
- **MCP server**: `ccpeek mcp` (stdio) exposing the same ops as MCP tools, so
  Claude Code / Pi / Cursor can be configured to query history natively.

Core operations (same shapes on all three transports):

| Op             | Answers                                                                                                                         |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------- |
| `search`       | "have I solved this before?" — FTS with agent/project/model/tool/date filters; every hit resolves to a session deep-link        |
| `sessions`     | list/filter sessions — the primary op (project facet, agent, date range, min cost, …)                                           |
| `session`      | one session with everything related to it: usage rollup, relations (forks, resumes, sidechains), linked artifacts with evidence |
| `transcript`   | one session as compact markdown or structured JSON, with seq ranges and limits (token-budget friendly)                          |
| `usage`        | token/cost aggregates grouped by day, model, project, or agent                                                                  |
| `file-history` | which sessions touched a path, when, and what changed                                                                           |
| `commands`     | shell-command history (v1's export, now also JSON)                                                                              |
| `scan`         | secret findings as JSON (v1's `scan --format json`, folded into the same layer)                                                 |

Design rules that make it agent-friendly rather than merely machine-readable:

- **Stable, versioned payloads** — a `"schema"` field in every response;
  additive-only evolution within a major version; golden tests on the shapes.
- **Token-efficient defaults** — snippets not blobs, `--limit` on everything,
  explicit `--full` to opt into large content. An agent asking "what did we
  do about auth last month" should get back hundreds of tokens, not a
  transcript dump.
- **Deterministic exit codes** — 0 results found, 1 error, 2 scan findings
  (kept from v1), 3 valid query but no matches — so scripts and agents can
  branch without parsing.
- **Self-describing** — `ccpeek docs --agents` prints an llms.txt-style
  cheatsheet of ops, flags, and schemas; `ccpeek skill install` drops a
  ready-made skill file into `~/.claude/skills` (and equivalents for other
  harnesses) so agents discover the tool without hand-written prompts.
- **Local-only** — the API binds 127.0.0.1 like everything else; MCP is
  stdio. Nothing is ever exposed off-machine.

Rollout: the query layer lands in P1 (the web UI is rebuilt on it anyway);
`ccpeek query` + `/api/v1` ship with v2.0 (P2); the MCP server and skill
packaging land in P3, when multi-agent data makes them most valuable.

---

## 6. Agent support matrix

The **launch set** — Claude Code, Pi, Codex, OpenCode, Cursor — targets ~80%
of coding-agent users. Everything below the line ships post-launch, prioritized
by demand. Locations shown are platform defaults; each adapter also honors the
agent's own relocation env vars (`CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR`,
`CODEX_HOME`, `OPENCODE_DATA_DIR`, …) per the root-discovery rules in §5.1.

| Agent            | Location                                            | Format                                                  | Usage data                                     | Phase                           |
| ---------------- | --------------------------------------------------- | ------------------------------------------------------- | ---------------------------------------------- | ------------------------------- |
| Claude Code      | `~/.claude` (12 source types)                       | JSONL + sidecars                                        | full (`message.usage`)                         | P1 (adapter = v1 port)          |
| Pi               | `~/.pi/agent/sessions/--<cwd>--/<ts>_<uuid>.jsonl`  | JSONL, typed entries, **documented + versioned spec**   | full tokens **+ pre-computed cost**            | P1 (second first-class adapter) |
| OpenAI Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`      | JSONL event stream                                      | cumulative `token_count`                       | P3                              |
| OpenCode         | `~/.local/share/opencode/storage/{session,message}` | JSON per message                                        | tokens + cost                                  | P3                              |
| Cursor CLI       | `~/.cursor/chats/{ws-hash}/{session-uuid}/store.db` | SQLite per session (`meta` + `blobs`, hex-encoded JSON) | usage metadata present; verify fields in spike | P3                              |
| — post-launch —  |                                                     |                                                         |                                                |                                 |
| Gemini CLI       | `~/.gemini/tmp/<hash>/chats/*.json`                 | JSON checkpoints                                        | per-turn tokens                                | backlog (first in line)         |
| Factory Droid    | `~/.factory/sessions`                               | JSONL per workspace                                     | verify in spike                                | backlog                         |
| Amp              | file-based threads                                  | verify in spike                                         | verify in spike                                | backlog                         |
| Aider            | `.aider.chat.history.md` (per repo)                 | markdown                                                | weak                                           | backlog                         |

Notes:

- **Why Pi is first-class and lands in P1.** Pi is the rare agent with an
  officially documented, versioned session format
  (`packages/coding-agent/docs/session-format.md`, header `version` 1–3),
  making it the lowest-risk adapter. Its data model also exercises v2's
  canonical schema harder than Claude's does: sessions are a **tree**
  (`id`/`parentId` per entry, in-place branching → maps to
  `messages.parent_external_id` and the P1 conversation-tree view),
  `model_change` entries mean the model dimension is per-entry rather than
  per-message-field, `compaction` / `branch_summary` / `label` /
  `session_info` entries map to `artifacts` kinds, and forked sessions carry
  `parentSession` in the header. Its per-message `usage` includes both tokens
  and a pre-computed cost breakdown — the first real consumer of the cost
  engine's `auto` (reported-vs-calculated) mode. Building Claude + Pi together
  in P1 validates the adapter interface before the messier P3 formats.
- Each P3 adapter starts with a **format spike**: collect real fixtures across
  agent versions into `testdata/<agent>/`, then implement against fixtures.
  These formats are unstable and undocumented — version-tolerant parsing +
  ingest diagnostics (which v1 already has) are the mitigation.
- **Cursor is the only non-file-based source** in the launch set: one SQLite
  DB per session with hex-encoded (not encrypted) JSON in `meta`/`blobs`
  tables. It still fits the adapter model — a `SourceRef` is the per-session
  `store.db` file and change detection hashes it like any other file — but
  P0 must design `SourceRef`/`Parse` so a source can be "a database to query,"
  not only "a text file to scan." Designing for this early keeps the door
  open for other SQLite-based agents later.
- **Gemini CLI deletes sessions after 30 days by default** — ccpeek ingesting
  them becomes a permanent archive, which is why Gemini is first in line
  post-launch. Market this: "your agent history outlives the agent's
  retention policy." Same argument applies to v1's deleted-source retention
  generally.
- Secret scanning runs across **all** agents' logs — a genuinely new
  capability (nothing else scans your Codex/Gemini history for leaked keys).

---

## 7. Feature plan

### P0 — must-have for v2.0 (parity + the point of v2)

1. **Everything v1 does today** (all 12 Claude sources, scan, exports,
   ingest diagnostics, compare, search) on the new engine — rebuilt as the
   v2 SPA with URL-compatible routes, virtualized transcripts for large
   sessions, v1's keyboard-first UX, and a ⌘K command palette.
   **Navigation is session-first**: the primary surface is a filterable
   session stream (agent, project facet, model, date, cost); a session's
   page gathers everything related to it (transcript, usage, artifacts,
   forks/sidechains). "Projects" survives as one grouping facet over
   sessions — no longer the entry-point hierarchy.
2. **Real tokens & cost**
   - Session header: real tokens by type (input/output/cache-w/cache-r),
     cost, model mix.
   - Per-message usage on hover/expand in the transcript.
   - Dashboard: spend today / this week / this month; tokens by day
     (stacked by type); cost by model; cost by project; cache-hit ratio and
     **cache savings** ("cache reads saved you ~$X vs. uncached input").
   - **Cost explorer** page: group by day|week|month × model × project ×
     agent; CSV/JSON export.
   - **Blocks view**: usage inside fixed UTC-aligned 5-hour windows (an
     approximation of subscription quota windows, which anchor to first
     activity), burn-rate within the current block.
   - `ccpeek usage --today|--month --json` CLI for scripts/statusline
     integration.
3. **Migration** (§8) — automatic, reported, reversible.
4. **Agent query basics** — `ccpeek query {search,sessions,transcript,usage}
--json` plus `/api/v1` on the local server, with stable schemas, limits,
   and exit codes (§5.7). Cheap to ship with v2.0 because the same query
   layer backs the UI.

### P1 — high value, fast follow

5. **Conversation tree view** — render branching: Claude's `parentUuid`
   (forks, resumed sessions) and Pi's native `id`/`parentId` tree with
   in-place branches, compactions, and labels; plus **sidechains**: link
   `Task` tool calls to their subagent transcripts (v1 drops all of this
   today).
6. **Cost per outcome** — join usage with Claude's own `usage_facets`
   (outcome/helpfulness/friction): "fully_achieved sessions averaged $0.42;
   abandoned ones $1.90." No other tool can do this.
7. **File-touch history** — per-file timeline across all sessions/agents:
   "what has touched `internal/store/schema.go`, when, and what did it cost."
8. **Budgets & alerts** — monthly budget per scope (global/project);
   dashboard banner + optional desktop notification at thresholds.
9. **Tool analytics** — error rates per tool, retry patterns, slowest tools,
   most-edited files, command frequency (feeding the existing shell-history
   export).
10. **Live session tail** (SSE) + fsnotify watch (§5.5).

### P2 — differentiators

11. **Agent surface completion** — `ccpeek mcp` (stdio MCP server over the
    query layer) plus self-describing docs: `ccpeek docs --agents`
    (llms.txt-style cheatsheet) and `ccpeek skill install` (drops a skill
    file into `~/.claude/skills` and equivalents) so harnesses discover the
    tool without hand-written prompts (§5.7).
12. **Archive & rescue** — `ccpeek archive export/import` (compressed bundle
    of raw sources + user state) for backup and machine moves; also sets up
    the retention-rescue story for the post-launch Gemini adapter (30-day
    auto-deletion).
13. **Cross-agent compare** — same repo, different agents: sessions, tokens,
    cost, tools side-by-side.
14. **Session replay** — timeline scrubber through a session's tool calls and
    diffs.
15. **Redaction-aware sharing** — export a session to standalone HTML/markdown
    with secret-scan matches redacted by default.

### Backlog / explicitly deferred

- Post-launch adapters: Gemini CLI (first in line — retention-rescue story),
  Factory Droid, Amp, Aider · semantic search (local embeddings, opt-in) ·
  TUI (`ccpeek top` live cost meter) · git commit correlation · multi-machine
  merge.

---

## 8. Migration plan (v1 → v2)

The user-facing contract: **upgrade is automatic, nothing is lost, rollback is
running the old version.**

### 8.1 Data migration

The v2 schema is a **clean break** — a full redesign per §5.2, with no
in-place evolution of v1 tables. That freedom is affordable because migration
is automatic and total.

Principle: the DB is a derived index of the agents' source directories; the
primary migration is **re-ingest from source** with the v2 engine. Exactly two
categories are not re-derivable and are **imported from the v1 DB**:

1. **Retained rows whose source files no longer exist on disk** (v1's
   deleted-source retention). Imported and tagged `origin='imported-v1'`.
2. **User state**: `scan_findings.ignored` flags — imported into
   `user_annotations` keyed by natural keys (rule_id + source natural key),
   not rowids, so they re-attach correctly after re-ingest.

The migration **triggers automatically**: on startup, v2 looks for its own DB;
if absent, it runs the full flow below with progress output before serving.
No flag, no prompt, no manual step. The `ccpeek migrate` command exists only
to re-run or troubleshoot it.

```text
1. v2 opens NEW file: $XDG_DATA_HOME/ccpeek/ccpeek2.db   (v1 ccpeek.db untouched)
2. Full ingest of all detected agent roots with the v2 engine
3. If ccpeek.db (v1) exists:
   a. read-only ATTACH
   b. import sessions/artifacts whose source_path is absent on disk → origin='imported-v1'
   c. import ignored-finding flags → user_annotations
4. Print + persist a migration report (counts: ingested, imported, annotations,
   anything skipped and why — reuses ingest_runs/ingest_issues)
5. v1 DB left in place. `ccpeek migrate cleanup` deletes it later, on request.
```

The same guarantee holds **within v2.x**: later schema changes ship as
sequential versioned migrations applied automatically at open (with the
fixture-drift CI carried over from v1) — the schema stays free to evolve
without ever asking the user to do anything. The v1→v2 clean break is a
one-time exception justified by the re-ingest path; within v2, migrations are
additive and the "always re-run initialSchema + backfill at open" anti-patterns
from v1 (§2.2 items 2–3) are explicitly banned.

Rollback: run the previous binary — its DB was never modified. Homebrew
(`brew install ccpeek@1` formula kept in the tap), Nix (pin the flake rev), and
GitHub release tarballs all provide the old version indefinitely.

### 8.2 Compatibility guarantees

- **CLI**: all v1 flags and subcommands keep working (`--claude-dir` maps to
  the Claude adapter's root; deprecated aliases warn, never break, for all of
  v2.x). `export commands` output stays byte-identical — port the existing
  zsh/bash/fish format tests verbatim.
- **URLs**: v1 routes 301 to their v2 equivalents (people bookmark
  conversations). Session pages move to session-centric URLs —
  `/projects/{dirName}/{sessionId}/` → `/sessions/{sessionId}/` — with the
  project surviving as a filter (`/sessions/?project=…`). A route-map table
  with golden tests.
- **Exit codes**: `scan` keeps exit 2 on findings.
- **DB file**: new name (`ccpeek2.db`), so no version of either binary can
  corrupt the other's store.

### 8.3 Verification of the migration itself

- **Upgrade-path CI job**: build the last v1 tag, ingest `testdata/`, mutate
  it (delete a source file, ignore a finding), then run v2 migrate and assert:
  every v1 row is present in v2 (by natural key), deleted-source rows carry
  `origin='imported-v1'`, ignore flags re-attached, report counts match.
- **Property check**: for any (v1 DB, source dir) pair, v2 session/artifact
  count ≥ v1 count; no natural key lost.
- **Fixture DBs**: extend `scripts/regenerate-migration-fixtures.sh` to
  produce a real v1.10 fixture DB used by the upgrade-path job.
- **Beta soak**: `v2.0.0-beta.N` GitHub pre-releases (not pushed to Homebrew);
  migration report asks beta users to file anomalies. Stable v2.0.0 updates
  the tap.

---

## 9. Roadmap

| Phase                      | Scope                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                            | Exit criteria                                                                                                                                                       | Est.    |
| -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| **P0 Foundations**         | modernc.org/sqlite benchmark vs CGO (ingest + FTS on a large real `~/.claude`); finalize schema v2 + adapter interface (ADRs in `docs/`); SPA scaffold spike (React-vs-Solid call, Vite dev proxy → Go API workflow, go:embed pipeline); pricing snapshot tooling; fixture corpus layout `testdata/<agent>/`                                                                                                                                                                                                     | ADRs merged; go/no-go on pure-Go SQLite; SPA stack locked; schema v2 reviewed                                                                                       | ~1–2 wk |
| **P1 Core engine**         | `internal/agent` + `internal/ingest` rewrite; Claude adapter at full v1 parity; **Pi adapter** (second first-class implementation, proves the framework); root discovery incl. agent env overrides (`CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR`, …); usage capture + pricing + cost engine + rollups; typed `internal/query` layer + `/api/v1` (§5.7); migration command + compat shims; **SPA parity build** (all v1 pages as React routes over the API, virtualized transcripts, keyboard nav); upgrade-path CI | all v1 e2e specs ported and green against the SPA; Pi fixture corpus ingests green (tokens + reported cost); migration job green; real cost visible on session page | ~4–5 wk |
| **P2 Cost & analytics UI** | dashboard v2 (spend tiles, stacked token timeline, cache savings); cost explorer with cross-filtering + brush/zoom (ECharts) + CSV/JSON; blocks view; budgets/alerts; agent surface v1: `ccpeek query` + `ccpeek usage` JSON CLIs (§5.7); command palette polish; pagination/virtualization everywhere                                                                                                                                                                                                           | ship `v2.0.0` (beta → stable)                                                                                                                                       | ~2–3 wk |
| **P3 Multi-agent**         | Codex, OpenCode, and Cursor adapters (spike → fixtures → implement; Cursor spike includes the SQLite `SourceRef` path); agent dimension in all UI filters; unified timeline; cross-agent compare; MCP server + skill packaging over the query layer (§5.7)                                                                                                                                                                                                                                                       | launch set complete: 5 adapters green on fixture corpus + real-world soak; `v2.1.0`                                                                                 | ~3 wk   |
| **P4 Live & power**        | fsnotify + SSE live tail; conversation tree + sidechains; file-touch history; archive/rescue; replay; redacted sharing                                                                                                                                                                                                                                                                                                                                                                                           | rolling `v2.x` releases                                                                                                                                             | ongoing |

Parallel track — **v1 quick wins** (ship as v1.10.x while P0/P1 run, all
low-risk):

- Guard the `role[:1]` export panic; add missing timestamp indexes; gate
  startup backfills behind a schema-version check (biggest perceived-perf win
  in v1); label estimated tokens as "~chars/4 estimate" in the UI; paginate
  the worst list pages.

---

## 10. Risks & mitigations

| Risk                                                                            | Mitigation                                                                                                                                                                 |
| ------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| modernc.org/sqlite slower or FTS5 gaps                                          | P0 benchmark gate; fallback = keep CGO driver (plan otherwise unchanged; driver stays behind sqlx)                                                                         |
| Other agents' formats change without notice (only Pi's is documented/versioned) | version-tolerant parsers; per-version fixture corpus; ingest diagnostics surface unknown shapes as warnings, never hard failures                                           |
| Usage double-counting (resumed/forked sessions, cumulative counters)            | dedupe by (external message id, request id); delta derivation with reset detection; unit fixtures for resume/fork cases                                                    |
| Pricing wrong or stale                                                          | embedded snapshot, refreshed at build time by `scripts/update-pricing.sh`; unknown models shown as "unpriced," never $0; costs labeled as estimates for subscription users |
| Rewrite stalls / scope creep                                                    | phases each ship; v1 stays maintained (quick-wins track) until v2.0 stable; P1 exit = v1 e2e suite green on the new engine                                                 |
| SPA rewrite balloons P1                                                         | parity pages first (lists/tables are fast in a component system), analytics deferred to P2; ported e2e suite is the objective gate; P0 spike de-risks the scaffold         |
| SPA regresses v1's speed/lightness                                              | localhost latency ≈ 0; code-splitting + prefetch + virtualization; strict CSP and embedded assets kept; perceived-perf checks in e2e                                       |
| Migration bugs lose user data                                                   | new DB file (v1 untouched); upgrade-path CI; natural-key imports; beta soak before tap update                                                                              |
| Large histories (multi-GB) strain SQLite/UI                                     | per-file transactions, rollups, server-side pagination, FTS-only text storage; optional content compression later                                                          |

---

## 11. Open questions

1. **Rename?** Recommendation: keep `ccpeek` through v2; revisit after P3.
2. **SPA framework**: React + TanStack (recommended: ecosystem depth,
   agent-assisted dev velocity) vs Solid/Svelte (lighter, faster). Decide in
   the P0 spike; the `/api/v1` contract contains the blast radius.
3. **Secret-scan scope**: scan non-Claude agent logs by default (recommended)
   or opt-in per agent?
4. **Subscription cost framing**: show `$` estimates by default for
   Pro/Max-only users, or default to token counts with `$` opt-in?
5. **Post-launch adapter order**: Gemini CLI is penciled in first (retention
   rescue), then Droid/Amp — confirm against issue feedback after v2.1.
