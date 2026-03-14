# CCPeek

Explore your Claude Code history. A local web app that indexes and browses your
Claude Code conversations, plans, todos, tasks, shell snapshots, file history,
paste cache, usage data, memories, and commands.

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
