# CCPeek v2 — Rewrite Plan

Status: proposal · Date: 2026-07-10

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
  Claude Code and Pi are the two first-class adapters built in Phase 1 —
  implementing the framework against two very different agents from day one is
  what proves the abstraction. Codex CLI, Gemini CLI, and OpenCode follow.
  Every entity gets an `agent` dimension.
- **Real token + cost accounting** becomes a first-class subsystem: capture
  `message.usage` (input/output/cache-write/cache-read/reasoning tokens) and
  `model` per message, price via an embedded LiteLLM/models.dev snapshot,
  aggregate into rollups for dashboards.
- **Migration is managed, not assumed.** The DB is a derived index, so the
  primary migration path is re-ingest from source — plus an explicit import
  step for the two things that are *not* re-derivable: rows whose source files
  were deleted (v1's retention feature) and user state (scan ignore flags).
  v2 writes a new DB file; v1's DB is never touched, so rollback is "run the
  old binary."

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
  The pattern is right; specific pages need work (see gaps).

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
   JSON re-parsing on *every* start.
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
3. Managed migration: no data loss, no user-state loss, trivial rollback.
4. Fix the structural debt (startup scans, duplication, memory, pagination).
5. Keep local-first privacy, single-binary distribution, and the existing
   quality bar (unit + e2e + migration-fixture CI).

**Non-goals (v2.0)**

- Cloud sync / multi-machine merge (design IDs so it's possible later).
- Team/server deployments, auth.
- Modifying or writing to any agent's data directory (strictly read-only).

---

## 4. Stack decision

| Option | Pros | Cons | Verdict |
|---|---|---|---|
| **Go (rewrite internals)** | Single static binary; existing release infra (Homebrew/Nix/4-arch) and test culture carry over; gitleaks is a Go library; maintainer fluency; pure-Go SQLite removes CGO | UI iteration slower than JS SPA ecosystems | **Recommended** |
| TypeScript (Bun/Node) | Richest UI ecosystem; fastest analytics-UI iteration; ccusage precedent | Distribution regresses (large `bun compile` binaries or runtime dependency); no gitleaks equivalent — secret scanning would be lost or shelled out; whole release pipeline rebuilt | No |
| Rust | Fast, single binary, no CGO | Slowest velocity; total rewrite incl. scanning; perf bottleneck is SQLite/IO anyway | No |

Within Go, three concrete changes:

- **`modernc.org/sqlite`** (pure Go, FTS5 included) replaces
  `mattn/go-sqlite3` + CGO. Phase 0 includes a benchmark gate (ingest + FTS
  query perf on a large real `~/.claude`); fallback is staying on CGO with the
  rest of the plan unchanged.
- **UI stays server-rendered** (`html/template`, Tailwind v4) with progressive
  enhancement. Add: SSE for live updates, a small vendored charting library
  (uPlot — ~40 kB, ideal for time series) instead of hand-rolled SVG, and
  pagination everywhere. No SPA framework; the analytics pages are the only
  chart-heavy surface and uPlot covers them.
- **Keep cobra, goldmark, chroma, gitleaks, difflib.** They earn their keep.

Naming: `ccpeek` stays the binary/brand ("**c**oding-**c**LI peek" once
multi-agent lands). A rename would burn Homebrew/Nix continuity for zero
functional gain. Revisit after Phase 3 if desired.

---

## 5. v2 architecture

### 5.1 Adapter framework

```go
// internal/agent
type Adapter interface {
    Slug() string                          // "claude-code", "pi", "codex", "gemini", "opencode"
    DetectRoots() []Root                   // default dirs + flag/env overrides
    Discover(root Root) ([]SourceRef, error) // files/dirs + content hashes
    Parse(src SourceRef, sink RecordSink) error // emit canonical records
    Watch(ctx context.Context, root Root, ch chan<- SourceRef) error // fsnotify
}

// RecordSink receives canonical records:
//   Session, Message, Usage, ToolCall, Artifact, HistoryEntry
```

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

### 5.2 Canonical schema (v2 sketch)

Design principles: (a) every entity carries `agent_id`; (b) **derived data and
user state live in disjoint table sets** — rebuild may drop the former, never
the latter; (c) natural keys (agent slug + external IDs) everywhere user state
or cross-references attach, so they survive re-ingest.

```
-- dimensions
agents(id, slug UNIQUE, display_name)
workspaces(id, canonical_path UNIQUE, display_name)        -- v1 "projects", agent-neutral
pricing(model_key, effective_from, input_per_mtok, output_per_mtok,
        cache_write_per_mtok, cache_read_per_mtok, source, fetched_at)

-- derived (rebuildable from sources)
sessions(id, agent_id, workspace_id, external_id, title, created_at,
         modified_at, git_branch, cwd, source_path, content_hash,
         origin DEFAULT 'ingest',            -- 'ingest' | 'imported-v1' | 'archive'
         UNIQUE(agent_id, external_id))
messages(id, session_id, seq, external_id, parent_external_id, role, kind,
         created_at, model, is_sidechain, content /* raw JSON */)
message_usage(message_id PK, input_tokens, output_tokens,
              cache_write_tokens, cache_read_tokens, reasoning_tokens,
              service_tier, reported_cost_usd, request_id)
tool_calls(id, session_id, message_id, seq, name, kind, input_json,
           result_status, result_excerpt, file_path, started_at)
artifacts(id, agent_id, workspace_id NULL, session_id NULL, kind, name,
          content, metadata_json, source_path, content_hash,
          UNIQUE(agent_id, kind, name))
  -- kinds: plan, todo_list, task_group, shell_snapshot, paste, memory,
  --        file_history, usage_facet, usage_report, checkpoint, ...
  -- structured children stay relational: todo_items, task_items, file_versions
history(id, agent_id, display, timestamp, project_path)
source_files(path PK, agent_id, content_hash, indexed_at)
ingest_runs / ingest_issues                                  -- carried from v1
rollup_usage_daily(day, agent_id, workspace_id, model,
                   sessions, messages, input_tokens, output_tokens,
                   cache_write_tokens, cache_read_tokens, cost_usd)
search_fts   -- ONE fts5 table (text + type/url metadata); messages keep raw
             -- JSON, extracted plain text lives only here (3× → 2×)

-- user state (NEVER dropped by rebuild/reset)
user_annotations(id, entity_type, natural_key, kind, value_json, created_at)
  -- kinds: scan_ignore, pin, note, tag, budget, saved_search
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

| Agent | Source of truth | Shape |
|---|---|---|
| Claude Code | `message.usage` + `message.model` on assistant JSONL lines | `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `service_tier`; older versions also wrote `costUSD` |
| Pi | `usage` on assistant message entries | `input`, `output`, `cacheRead`, `cacheWrite`, `totalTokens` **plus a pre-computed `cost` breakdown** (`cost.input/output/cacheRead/cacheWrite/total`); model tracked via `model_change` entries |
| Codex CLI | `token_count` events (cumulative) | subtract previous totals → per-turn input / cached input / output / reasoning; logs before 2025-09 have none |
| Gemini CLI | session checkpoint JSON | per-turn token stats incl. cached |
| OpenCode | per-message JSON | token + cost fields present |

**Correctness details that v1-style naive parsing would get wrong:**

- **Dedupe usage** by `(message external_id, request_id)` — resumed/forked
  Claude sessions duplicate assistant lines across JSONL files; without dedupe,
  costs double-count. (This is the same approach ccusage uses.)
- **Cumulative counters** (Codex) need delta derivation with reset detection.
- **Unknown models must be visible**: usage rows that can't be priced are
  aggregated as "unpriced tokens," never silently $0.

**Pricing:**

- Embed a snapshot of LiteLLM's `model_prices_and_context_window.json`
  (cross-provider, includes cache-write/read rates) at build time;
  `ccpeek pricing update` refreshes into the `pricing` table at runtime.
  models.dev is the fallback source. Fully offline-capable.
- Model-key normalization layer (`claude-sonnet-5` ≡ `anthropic/claude-sonnet-5`
  ≡ Bedrock/Vertex ids).
- Cost is **computed from tokens at query time** against the pricing table
  (respecting `effective_from`), then materialized into `rollup_usage_daily`
  for dashboard speed; rollups invalidate when pricing changes. Where the agent
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
- **SSE endpoint** (`/events`) pushes "data changed" to the browser; pages
  refresh their data (list pages re-fetch, dashboard tiles update).
- Headline feature: **live session tail** — open a running session and watch
  messages/tool calls stream in. This is the "peek" the name promises.

### 5.6 Performance targets (gate in CI where practical)

- Warm start (schema current): **< 200 ms** to listening (no backfills).
- Ingest: per-source-file transactions with batched multi-row inserts;
  1 GB of JSONL **< 60 s**; bounded memory (stream sessions, don't hold a
  project's full message set).
- Dashboard queries **< 50 ms** from rollups; every list paginated server-side.

---

## 6. Agent support matrix

| Agent | Location | Format | Usage data | Phase |
|---|---|---|---|---|
| Claude Code | `~/.claude` (12 source types) | JSONL + sidecars | full (`message.usage`) | P1 (adapter = v1 port) |
| Pi | `~/.pi/agent/sessions/--<cwd>--/<ts>_<uuid>.jsonl` | JSONL, typed entries, **documented + versioned spec** | full tokens **+ pre-computed cost** | P1 (second first-class adapter) |
| OpenAI Codex CLI | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` | JSONL event stream | cumulative `token_count` | P3 |
| Gemini CLI | `~/.gemini/tmp/<hash>/chats/*.json` | JSON checkpoints | per-turn tokens | P3 |
| OpenCode | `~/.local/share/opencode/storage/{session,message}` | JSON per message | tokens + cost | P3 |
| Factory Droid | `~/.factory/sessions` | JSONL per workspace | verify in spike | P3 stretch |
| Amp | file-based threads | verify in spike | verify in spike | P3 stretch |
| Cursor CLI | `~/.cursor/chats` (SQLite) | SQLite | verify in spike | P4 |
| Aider | `.aider.chat.history.md` (per repo) | markdown | weak | backlog |

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
- **Gemini CLI deletes sessions after 30 days by default** — ccpeek ingesting
  them becomes a permanent archive. Market this: "your agent history outlives
  the agent's retention policy." Same argument applies to v1's
  deleted-source retention generally.
- Secret scanning runs across **all** agents' logs — a genuinely new
  capability (nothing else scans your Codex/Gemini history for leaked keys).

---

## 7. Feature plan

### P0 — must-have for v2.0 (parity + the point of v2)

1. **Everything v1 does today** (all 12 Claude sources, scan, exports,
   ingest diagnostics, compare, search) on the new engine.
2. **Real tokens & cost**
   - Session header: real tokens by type (input/output/cache-w/cache-r),
     cost, model mix.
   - Per-message usage on hover/expand in the transcript.
   - Dashboard: spend today / this week / this month; tokens by day
     (stacked by type); cost by model; cost by project; cache-hit ratio and
     **cache savings** ("cache reads saved you ~$X vs. uncached input").
   - **Cost explorer** page: group by day|week|month × model × project ×
     agent; CSV/JSON export.
   - **Blocks view**: usage inside 5-hour quota windows (how Claude
     subscription limits are actually experienced), burn-rate within the
     current block.
   - `ccpeek usage --today|--month --json` CLI for scripts/statusline
     integration.
3. **Migration** (§8) — automatic, reported, reversible.

### P1 — high value, fast follow

4. **Conversation tree view** — render branching: Claude's `parentUuid`
   (forks, resumed sessions) and Pi's native `id`/`parentId` tree with
   in-place branches, compactions, and labels; plus **sidechains**: link
   `Task` tool calls to their subagent transcripts (v1 drops all of this
   today).
5. **Cost per outcome** — join usage with Claude's own `usage_facets`
   (outcome/helpfulness/friction): "fully_achieved sessions averaged $0.42;
   abandoned ones $1.90." No other tool can do this.
6. **File-touch history** — per-file timeline across all sessions/agents:
   "what has touched `internal/store/schema.go`, when, and what did it cost."
7. **Budgets & alerts** — monthly budget per scope (global/project);
   dashboard banner + optional desktop notification at thresholds.
8. **Tool analytics** — error rates per tool, retry patterns, slowest tools,
   most-edited files, command frequency (feeding the existing shell-history
   export).
9. **Live session tail** (SSE) + fsnotify watch (§5.5).

### P2 — differentiators

10. **MCP server mode** (`ccpeek mcp`) — expose search/usage/history as MCP
    tools so any agent can query your past sessions ("have I solved this
    before?"). Local stdio transport only.
11. **Archive & rescue** — `ccpeek archive export/import` (compressed bundle
    of raw sources + user state) for backup and machine moves; positions the
    Gemini 30-day rescue story.
12. **Cross-agent compare** — same repo, different agents: sessions, tokens,
    cost, tools side-by-side.
13. **Session replay** — timeline scrubber through a session's tool calls and
    diffs.
14. **Redaction-aware sharing** — export a session to standalone HTML/markdown
    with secret-scan matches redacted by default.

### Backlog / explicitly deferred

- Semantic search (local embeddings, opt-in) · TUI (`ccpeek top` live cost
  meter) · git commit correlation · multi-machine merge · Aider/Cursor
  adapters (pending spikes).

---

## 8. Migration plan (v1 → v2)

The user-facing contract: **upgrade is automatic, nothing is lost, rollback is
running the old version.**

### 8.1 Data migration

Principle: the DB is a derived index of `~/.claude`; the primary migration is
**re-ingest from source** with the v2 engine. Exactly two categories are not
re-derivable and are **imported from the v1 DB**:

1. **Retained rows whose source files no longer exist on disk** (v1's
   deleted-source retention). Imported and tagged `origin='imported-v1'`.
2. **User state**: `scan_findings.ignored` flags — imported into
   `user_annotations` keyed by natural keys (rule_id + source natural key),
   not rowids, so they re-attach correctly after re-ingest.

Flow on first v2 start (also available explicitly as `ccpeek migrate`):

```
1. v2 opens NEW file: $XDG_DATA_HOME/ccpeek/ccpeek2.db   (v1 ccpeek.db untouched)
2. Full ingest of ~/.claude with the v2 engine
3. If ccpeek.db (v1) exists:
   a. read-only ATTACH
   b. import sessions/artifacts whose source_path is absent on disk → origin='imported-v1'
   c. import ignored-finding flags → user_annotations
4. Print + persist a migration report (counts: ingested, imported, annotations,
   anything skipped and why — reuses ingest_runs/ingest_issues)
5. v1 DB left in place. `ccpeek migrate cleanup` deletes it later, on request.
```

Rollback: run the previous binary — its DB was never modified. Homebrew
(`brew install ccpeek@1` formula kept in the tap), Nix (pin the flake rev), and
GitHub release tarballs all provide the old version indefinitely.

### 8.2 Compatibility guarantees

- **CLI**: all v1 flags and subcommands keep working (`--claude-dir` maps to
  the Claude adapter's root; deprecated aliases warn, never break, for all of
  v2.x). `export commands` output stays byte-identical — port the existing
  zsh/bash/fish format tests verbatim.
- **URLs**: v1 routes 301 to their v2 equivalents (people bookmark
  conversations). A route-map table with golden tests.
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

| Phase | Scope | Exit criteria | Est. |
|---|---|---|---|
| **P0 Foundations** | modernc.org/sqlite benchmark vs CGO (ingest + FTS on a large real `~/.claude`); finalize schema v2 + adapter interface (ADRs in `docs/`); pricing snapshot tooling; fixture corpus layout `testdata/<agent>/` | ADRs merged; go/no-go on pure-Go SQLite; schema v2 reviewed | ~1 wk |
| **P1 Core engine** | `internal/agent` + `internal/ingest` rewrite; Claude adapter at full v1 parity; **Pi adapter** (second first-class implementation, proves the framework); usage capture + pricing + cost engine + rollups; migration command + compat shims; existing UI wired to new store; upgrade-path CI | all v1 e2e tests green on v2 engine; Pi fixture corpus ingests green (tokens + reported cost); migration job green; real cost visible on session page | ~3–4 wk |
| **P2 Cost & analytics UI** | dashboard v2 (spend tiles, stacked token timeline, cache savings); cost explorer + CSV/JSON; blocks view; budgets/alerts; `ccpeek usage` CLI; pagination everywhere; uPlot charts with keyboard/ARIA support | ship `v2.0.0` (beta → stable) | ~2 wk |
| **P3 Multi-agent** | Codex, Gemini, OpenCode adapters (spike → fixtures → implement); agent dimension in all UI filters; unified timeline; cross-agent compare; Droid/Amp stretch | 3 adapters green on fixture corpus + real-world soak; `v2.1.0` | ~3 wk |
| **P4 Live & power** | fsnotify + SSE live tail; conversation tree + sidechains; MCP server; file-touch history; archive/rescue; replay; redacted sharing | rolling `v2.x` releases | ongoing |

Parallel track — **v1 quick wins** (ship as v1.10.x while P0/P1 run, all
low-risk):

- Guard the `role[:1]` export panic; add missing timestamp indexes; gate
  startup backfills behind a schema-version check (biggest perceived-perf win
  in v1); label estimated tokens as "~chars/4 estimate" in the UI; paginate
  the worst list pages.

---

## 10. Risks & mitigations

| Risk | Mitigation |
|---|---|
| modernc.org/sqlite slower or FTS5 gaps | P0 benchmark gate; fallback = keep CGO driver (plan otherwise unchanged; driver stays behind sqlx) |
| Other agents' formats change without notice (none are documented/stable) | version-tolerant parsers; per-version fixture corpus; ingest diagnostics surface unknown shapes as warnings, never hard failures |
| Usage double-counting (resumed/forked sessions, cumulative counters) | dedupe by (external message id, request id); delta derivation with reset detection; unit fixtures for resume/fork cases |
| Pricing wrong or stale | embedded snapshot + `pricing update`; unknown models shown as "unpriced," never $0; costs labeled as estimates for subscription users |
| Rewrite stalls / scope creep | phases each ship; v1 stays maintained (quick-wins track) until v2.0 stable; P1 exit = v1 e2e suite green on the new engine |
| Migration bugs lose user data | new DB file (v1 untouched); upgrade-path CI; natural-key imports; beta soak before tap update |
| Large histories (multi-GB) strain SQLite/UI | per-file transactions, rollups, server-side pagination, FTS-only text storage; optional content compression later |

---

## 11. Open questions

1. **Rename?** Recommendation: keep `ccpeek` through v2; revisit after P3.
2. **Secret-scan scope**: scan non-Claude agent logs by default (recommended)
   or opt-in per agent?
3. **Subscription cost framing**: show `$` estimates by default for
   Pro/Max-only users, or default to token counts with `$` opt-in?
4. **Droid/Amp/Cursor priority**: which stretch adapter matters most to actual
   users? Decide from issue feedback after v2.0.
