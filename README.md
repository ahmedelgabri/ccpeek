# CCPeek

Explore your Claude Code history. A local web app that indexes and browses your
Claude Code conversations, plans, todos, tasks, shell snapshots, file history,
paste cache, usage data, memories, and commands.

> **v2 preview.** This branch also ships the v2 engine: a session-centric
> index of **Claude Code, Pi, Codex CLI, OpenCode, and Cursor** with real
> token usage and estimated cost, an agent-facing query surface
> (`ccpeek query`, `/api/v1`, `ccpeek mcp`), live updates, and a new UI at
> `/v2/`. It builds itself automatically on first run (your v1 database is
> never modified) — see [docs/v2-plan.md](docs/v2-plan.md) and the
> [v2 section](#v2-preview) below.

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

### Go install

Requires Go 1.25+, Node.js, and pnpm (for building CSS):

```sh
# Clone and build
git clone https://github.com/ahmedelgabri/ccpeek.git
cd ccpeek
pnpm install
just build
# Binary is at cmd/ccpeek/ccpeek
```

### Pre-built binaries

Download from [GitHub Releases](https://github.com/ahmedelgabri/ccpeek/releases).
Archives include shell completions and man pages.

## Usage

```sh
# Index ~/.claude and start the web UI
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

The server reads Claude Code data from `~/.claude`, writes an index to
`~/.local/share/ccpeek/ccpeek.db` (respects `$XDG_DATA_HOME`), and serves the
web UI at `http://localhost:3000`.

### Flags

| Flag           | Default                           | Description                                        |
| -------------- | --------------------------------- | -------------------------------------------------- |
| `-p`, `--port` | `3000`                            | Server port                                        |
| `--claude-dir` | `~/.claude`                       | Source directory (Claude data)                     |
| `--data-file`  | `~/.local/share/ccpeek/ccpeek.db` | SQLite database file path                          |
| `--skip-index` | `false`                           | Skip indexing, serve existing data                 |
| `--index-only` | `false`                           | Index and exit                                     |
| `--open`       | `false`                           | Open browser after starting                        |
| `--watch`      | `false`                           | Re-index periodically while serving                |
| `--rebuild`    | `false`                           | Force full rebuild (drop all data and re-index)    |
| `--prune`      | `false`                           | Remove data from source files that no longer exist |
| `--skip-scan`  | `false`                           | Skip secret scanning after indexing                |

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
and viewable in the web UI at `/scan/`.

```sh
ccpeek scan
```

#### `ccpeek export commands`

Export bash commands extracted from Claude Code sessions in shell history format.

```sh
# Plain (one command per line)
ccpeek export commands

# Append to zsh history
ccpeek export commands --format zsh >> ~/.zsh_history && fc -R

# Append to bash history
ccpeek export commands --format bash >> ~/.bash_history && history -r

# Append to fish history
ccpeek export commands --format fish >> ~/.local/share/fish/fish_history

# Filter by project or date range
ccpeek export commands --project myapp --from 2025-01-01 --to 2025-06-01
```

## What it indexes

- **Projects** - Conversations grouped by project directory
- **Plans** - Markdown plan files from Claude sessions
- **Shell Snapshots** - Shell environment captures
- **Commands** - Bash commands extracted from sessions
- **Todos** - Task lists from Claude sessions
- **Tasks** - Task groups from Claude sessions
- **File History** - File backups from conversations
- **Paste Cache** - Pasted content from sessions
- **Usage Data** - Session usage insights and reports
- **Memories** - Project-level MEMORY.md context files
- **Secret Scan** - Detects leaked secrets across all indexed data

## v2 preview

The v2 engine indexes every supported agent into one session-centric
database (`ccpeek2.db`, alongside the v1 file) with real token usage and
estimated cost. It initializes automatically on first use: full ingest of
detected agent roots plus import of v1-only data (sessions whose source
files were deleted, scan-ignore flags). Rollback is running the old
version — the v1 database is opened read-only and never modified.

```sh
# Query as JSON (no server needed; exit 3 = valid query, no matches)
ccpeek query sessions --agent codex --since 2026-07-01
ccpeek query session claude-code <session-id>
ccpeek query transcript pi <session-id> --limit 50
ccpeek query usage --group model
ccpeek query search "rate limiting"

# Serve: v1 UI at /, v2 UI at /v2/, JSON API at /api/v1
# (--watch adds fsnotify-driven re-indexing + SSE live updates)
ccpeek --watch

# MCP server over stdio (register: claude mcp add ccpeek -- ccpeek mcp)
ccpeek mcp

# Secret-scan every agent's history (not just Claude's)
ccpeek scan --v2

# Cheatsheet for agents/scripts
ccpeek docs --agents
```

Agent data roots resolve as: explicit config > the agent's own env
override (`CLAUDE_CONFIG_DIR`, `PI_CODING_AGENT_DIR`, `CODEX_HOME`,
`OPENCODE_DATA_DIR`) > platform defaults.

## Development

Use [Nix](https://nixos.org/) with `nix develop` for a complete dev environment,
or install Go 1.25+, Node.js, pnpm, and [just](https://github.com/casey/just) manually.

```sh
pnpm install

# Run dev server (builds CSS, opens browser)
just dev

# Watch CSS changes
just css-watch

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
