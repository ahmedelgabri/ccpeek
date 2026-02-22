# CCExplore

Explore your Claude Code history. A local web app that indexes and browses your
Claude Code conversations, plans, todos, shell snapshots, and file history.

## Prerequisites

- [Go](https://go.dev/) 1.25+
- [Node.js](https://nodejs.org/) (for Tailwind CSS build)
- [pnpm](https://pnpm.io/)
- [mise](https://mise.jdx.dev/) (task runner)

## Setup

```sh
mise install
pnpm install
```

## Usage

```sh
# Index and start the server
mise run dev

# Or build the binary
mise run build
./cmd/ccexplore/ccexplore --open
```

The server reads Claude Code data from `~/.claude` and writes an index to
`$TMPDIR/.ccexplore`. On subsequent runs, use `--skip-index` to skip re-indexing.

### CLI flags

| Flag           | Default              | Description                       |
| -------------- | -------------------- | --------------------------------- |
| `--port`       | `3000`               | Server port                       |
| `--claude-dir` | `~/.claude`          | Source directory (Claude data)    |
| `--data-dir`   | `$TMPDIR/.ccexplore` | Indexed data output directory     |
| `--skip-index` | `false`              | Skip indexing, serve existing data|
| `--index-only` | `false`              | Index and exit                    |
| `--open`       | `false`              | Open browser after starting       |

## Development

```sh
# Watch CSS changes
mise run css:watch

# Run dev server
mise run dev

# Run all tests
mise run test

# Run unit tests only
mise run test:unit

# Run e2e tests only
mise run test:e2e

# Lint
mise run lint

# Format
mise run format
```

## What it indexes

- **Projects** - Conversations grouped by project directory
- **Plans** - Markdown plan files from Claude sessions
- **Shell Snapshots** - Shell environment captures
- **Todos** - Task lists from Claude sessions
- **File History** - File backups from conversations
