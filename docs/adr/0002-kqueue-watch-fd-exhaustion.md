# ADR-0002: Poll instead of fsnotify on kqueue platforms

Status: accepted · Date: 2026-08-05 · Phase: P4 (see `docs/v2-plan.md` §5.5)

## Context

v2 replaced v1's 30-second re-index ticker with fsnotify watchers over every directory under every resolved agent root (`ingest.Runner.Watch`). On Linux that maps to inotify, which watches a directory with a single descriptor. On macOS and the BSDs fsnotify's only backend is kqueue, and kqueue can only watch what it has an open file descriptor for — so `watcher.Add(dir)` opens an fd for the directory **and every file inside it**.

Against a real `~/.claude` tree (every transcript, subagent `.jsonl`, and backup) that is ~40–60k fds per watching process. `ccpeek mcp` watches unconditionally and Claude Code spawns one per session, so five concurrent sessions held ~220k fds against a default `kern.maxfiles` of 245,760 — system-wide fd exhaustion (`ENFILE`) that crashed unrelated apps (Chrome, shells, coreutils) and plausibly contributed to a memory-pressure watchdog panic on the same machine.

Options considered:

- **FSEvents** — the native macOS API watches trees recursively with no per-file fds, but the Go bindings (`fsnotify/fsevents`) require CGO and the CoreServices framework. ADR-0001 moved the store to pure Go specifically to keep `CGO_ENABLED=0` cross-compilation and working `go install`; a darwin-only CGO dependency reverses that.
- **Watch fewer directories** — transcripts live two levels deep and the leaf directories hold the thousands of files; any subset that still sees changes still opens the fds.
- **Poll on a fixed interval** — v1's model. Change detection is already a stat-only fingerprint sweep (size+mtime, no bytes read on match — `docs/incremental-indexing.md`), so a no-change pass costs metadata syscalls and holds no descriptors between passes. Simple, but up to 30 s of latency for everything, including the live session tail.
- **Watch only the hot files** — kqueue's cost is per watched _item_: `Add(file)` on an individual file is exactly one descriptor; it is `Add(directory)` that fans out to one per contained file. Sessions being appended to right now are a bounded set (dozens, not tens of thousands), so watching them individually restores instant append detection for the case that matters. New files are invisible to per-file watches, so a scan backstop is still required.

## Decision

On `darwin`, `freebsd`, `openbsd`, `netbsd`, and `dragonfly`, `Runner.Watch` runs a hybrid loop instead of recursive fsnotify watches:

- Individual kqueue watches on the files modified within the last 24 h, newest first, capped at 512 — one fd each. An event on any of them triggers a debounced pass, so appends to live sessions land near-instantly.
- An adaptive scan as the backstop for new files and late-appearing agents: every 2 s while passes are finding changes, decaying to every 30 s (v1's ticker default) after two quiet minutes.

Linux and Windows keep the fsnotify path.

## Consequences

- Steady-state fd usage on macOS drops from ~40–60k per serving process to at most 512 file watches plus the process baseline.
- Append latency on kqueue platforms stays ~instant (event debounce, 500 ms). A brand-new session file appears within ~2 s while the machine is active, and within 30 s at worst from a cold idle. The SSE/live-tail pipeline is unchanged; only the trigger differs.
- The scan re-resolves roots every pass, so the fsnotify path's 2-minute rescan tick (late-appearing agents) is subsumed on those platforms.
- The deprecated `--watch-interval` flag stays deprecated and ignored; the intervals are constants.
- Revisit if a pure-Go FSEvents binding (or an fsnotify FSEvents backend, fsnotify/fsnotify#11) becomes available — FSEvents would make everything instant with one stream and no per-file descriptors, but today's bindings require CGO.
