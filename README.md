# CCPeek

Explore your coding-agent history. A local web app that indexes
**Claude Code, Pi, Codex CLI, and OpenCode** sessions (plus
**Cursor**, experimental — see the capability matrix below) into one
session-centric database — conversations, plans, todos, tasks, shell
snapshots, file history, paste cache, memories, and commands — with real
token usage and estimated cost, plus an agent-facing query surface
(`ccpeek query`, `/api/v1`, `ccpeek mcp`) and live updates. Everything
stays on this machine.

https://github.com/user-attachments/assets/906eaae2-628f-49ae-8344-88b855792c30

## Installation

### Homebrew

```sh
brew install ahmedelgabri/tap/ccpeek
```

### Nix Flakes

```sh
# Run directly
nix run github:ahmedelgabri/ccpeek

# Or install into your profile
nix profile install github:ahmedelgabri/ccpeek
```

### Build from source (full product)

Requires Go 1.25+, Node.js, and pnpm (for building the web UI):

```sh
# Clone and build
git clone https://github.com/ahmedelgabri/ccpeek.git
cd ccpeek
pnpm install
just build
# Binary is at cmd/ccpeek/ccpeek
```

The full product is compiled with the `withui` build tag, which
enforces the embedded UI's presence at compile time — `just build` (and
every release path) cannot produce a UI-less binary.

### `go install` (API-only variant)

A plain `go build ./...` or `go install` cannot run the SPA build, so
it deliberately produces the **API-only variant**: `/api/v1`,
`ccpeek query`, and `ccpeek mcp` work normally, the server logs a
warning at startup, and `/` explains what is missing instead of
rendering a blank page. Use it for headless/agent-only setups; use any
other installation method for the web UI.

### Pre-built binaries

Download from [GitHub Releases](https://github.com/ahmedelgabri/ccpeek/releases).
Archives include shell completions and man pages.

## Usage

```sh
# Index detected agent roots and start the web UI
ccpeek

# Open browser automatically
ccpeek --open

# Use a different port
ccpeek --port 8080

# Skip re-indexing on subsequent runs
ccpeek --skip-index

# Index only (no server)
ccpeek --index-only
```

The server reads each agent's data from its default root (for Claude Code,
`~/.claude`), writes an index to `~/.local/share/ccpeek/ccpeek2.db`
(respects `$XDG_DATA_HOME`), and serves the web UI at
`http://localhost:3000`.

The port binds immediately; indexing runs behind it with progress on
stderr, and the UI fills in live as data lands (`/api/v1/ready` answers
200 once the first pass completes). After the first full build,
unchanged files are skipped via a size+mtime check without re-reading
them, so warm startups stay fast even on multi-GB histories.

### Flags

| Flag            | Default                            | Description                                                                                                           |
| --------------- | ---------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `-p`, `--port`  | `3000`                             | Server port                                                                                                           |
| `--claude-dir`  | `~/.claude`                        | Source directory (Claude data)                                                                                        |
| `--data-file`   | `~/.local/share/ccpeek/ccpeek2.db` | Database location — the index is derived from this path. A legacy database here is imported once; see the [FAQ](#faq) |
| `--skip-index`  | `false`                            | Skip indexing, serve existing data                                                                                    |
| `--index-only`  | `false`                            | Index and exit                                                                                                        |
| `-o`, `--open`  | `false`                            | Open browser after starting                                                                                           |
| `-w`, `--watch` | `false`                            | Re-index while serving, on filesystem changes                                                                         |
| `--rebuild`     | `false`                            | Force full rebuild (drop all data and re-index)                                                                       |
| `--prune`       | `false`                            | Remove data from source files that no longer exist                                                                    |
| `--skip-scan`   | `false`                            | Skip secret scanning after indexing                                                                                   |
| `-q`, `--quiet` | `false`                            | Suppress informational output                                                                                         |

### Shell completions

```sh
# Bash
ccpeek completion bash >/etc/bash_completion.d/ccpeek

# Zsh
ccpeek completion zsh >"${fpath[1]}/_ccpeek"

# Fish
ccpeek completion fish >~/.config/fish/completions/ccpeek.fish
```

Homebrew and Nix installations include completions and man pages automatically.

### Subcommands

#### `ccpeek scan`

Scan indexed data for leaked secrets, API keys, tokens, and passwords. Uses
gitleaks detection rules (150+ patterns). Results are stored in the database
and viewable in the web UI at `/scan`. The index refreshes incrementally
before the scan so newly written history is covered (`--no-index` opts out).

```sh
ccpeek scan
```

#### `ccpeek export commands`

Export shell commands extracted from indexed agent sessions in shell history format.

```sh
# Plain (one command per line)
ccpeek export commands

# Append to zsh history
ccpeek export commands --format zsh >> ~/.zsh_history && fc -R

# Append to bash history
ccpeek export commands --format bash >> ~/.bash_history && history -r

# Append to fish history
ccpeek export commands --format fish >> ~/.local/share/fish/fish_history

# Filter by workspace path or date range
ccpeek export commands --project myapp --from 2025-01-01 --to 2025-06-01
```

## What it indexes

- **Sessions** - Conversations from every supported agent, with tokens and cost
- **Artifacts** - Plans, todos, tasks, shell snapshots, paste cache, usage
  data, memories, and file history, linked to their sessions
- **Commands** - Shell commands extracted from sessions
- **Usage** - Token/cost rollups by day, model, workspace, and agent
- **Secret Scan** - Detects leaked secrets across all indexed data

### Agent capability matrix

| Agent       | Status           | Messages | Usage/cost | Tool calls | Notes                                                                                                                                 |
| ----------- | ---------------- | -------- | ---------- | ---------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| Claude Code | supported        | ✓        | ✓          | ✓          | Sessions, all sidecar artifacts, prompt history, incremental tail parsing                                                             |
| Pi          | supported        | ✓        | ✓          | ✓          | Documented session format; forks/branches; reported costs                                                                             |
| Codex CLI   | supported        | ✓        | ✓          | ✓          | Cumulative token counts recovered per turn; reasoning is a subset of output                                                           |
| OpenCode    | supported        | ✓        | ✓          | ✓          | Reported costs preferred; additive reasoning folded into billable output                                                              |
| Cursor      | **experimental** | ✓        | ✓          | —          | Schema derived from fixtures, not yet validated against a real `store.db`; no tool extraction. Expect gaps until real-data validation |

## Agent-facing surface

Every read is available three ways — `ccpeek query` (JSON on the
command line), `/api/v1` (HTTP), and `ccpeek mcp` (MCP over stdio) —
generated from one shared definition, so the surfaces never drift.

```sh
# Query as JSON (no server needed; exit 3 = valid query, no matches)
ccpeek query sessions --agent codex --since 2026-07-01
ccpeek query session claude-code <session-id>
ccpeek query transcript pi <session-id> --limit 50
ccpeek query usage --group model
ccpeek query search "rate limiting"

# Serve the UI + API (--watch adds fsnotify re-indexing + SSE live updates)
ccpeek --watch

# MCP server over stdio (register: claude mcp add ccpeek -- ccpeek mcp)
ccpeek mcp

# Secret-scan every agent's history (not just Claude's)
ccpeek scan

# Shell-history export, ingest diagnostics, agent cheatsheet
ccpeek export commands --format zsh
ccpeek ingest --latest
ccpeek docs --agents

# Install the ccpeek skill into ~/.claude/skills (or --dir for another harness)
ccpeek skill install
```

Agent data roots resolve as: explicit config > the agent's own env
override (`CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR`, `CODEX_HOME`,
`OPENCODE_DATA_DIR`; Cursor has none, so ccpeek honors
`CCPEEK_CURSOR_DIR`) > platform defaults.

## Development

Use [Nix](https://nixos.org/) with `nix develop` for a complete dev environment,
or install Go 1.25+, Node.js, pnpm, and [just](https://github.com/casey/just) manually.

```sh
pnpm install

# Run dev server (builds the UI, opens browser)
just dev

# Vite dev server with HMR against a running ccpeek
just ui-dev

# Run all tests
just test

# Run unit tests only
just test-unit

# Run e2e tests only
just test-e2e

# Lint
just lint

# Format
just format
```

## FAQ

### I used ccpeek before — what happens to my old (v1) data?

CCPeek is a session-centric, multi-agent rewrite of the original
single-agent tool, and it upgrades automatically on first run — no
steps, no flags. It ingests your detected agent roots into a new index
(`ccpeek2.db`, written alongside the old `ccpeek.db`) and imports the
v1-only data that cannot be re-derived from source files: sessions
whose sources were deleted, and your scan-ignore flags. The v1 database
is opened read-only and never modified, so rolling back is just running
the old version. See [docs/v2-plan.md](docs/v2-plan.md) for the full
design.

### Do my old bookmarks and URLs still work?

Yes. Every legacy URL (`/projects/…`, `/plans/`, `/commands/`, session
bookmarks, the `/v2/` preview mount) permanently redirects to its
session-centric equivalent.

### Where is the database stored, and what is `--data-file`?

The index lives at `~/.local/share/ccpeek/ccpeek2.db` (respecting
`$XDG_DATA_HOME`). `--data-file` names the legacy database path; the
current index is derived from it as a sibling (`ccpeek.db` →
`ccpeek2.db`, `x.db` → `x.v2.db`). A legacy file at that path is
imported once (read-only); don't point `--data-file` at a current
index.

### Why is `--watch-interval` ignored?

It's accepted for backward compatibility only. ccpeek re-indexes on
filesystem events (use `--watch`) rather than on a timer.
