# CCPeek

Explore your Claude Code history. A local web app that indexes and browses your
Claude Code conversations, plans, todos, shell snapshots, and file history.

https://github.com/user-attachments/assets/906eaae2-628f-49ae-8344-88b855792c30

## Installation

### Homebrew

```sh
brew install ahmedelgabri/ccpeek/ccpeek
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
`$TMPDIR/.ccpeek`, and serves the web UI at `http://localhost:3000`.

### Flags

| Flag           | Default           | Description                        |
| -------------- | ----------------- | ---------------------------------- |
| `-p`, `--port` | `3000`            | Server port                        |
| `--claude-dir` | `~/.claude`       | Source directory (Claude data)     |
| `--data-file`  | `$TMPDIR/.ccpeek/ccpeek.db` | SQLite database file path   |
| `--skip-index` | `false`           | Skip indexing, serve existing data |
| `--index-only` | `false`           | Index and exit                     |
| `--open`       | `false`           | Open browser after starting        |

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

## What it indexes

- **Projects** - Conversations grouped by project directory
- **Plans** - Markdown plan files from Claude sessions
- **Shell Snapshots** - Shell environment captures
- **Todos** - Task lists from Claude sessions
- **File History** - File backups from conversations

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
