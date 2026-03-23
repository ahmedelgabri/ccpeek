# AGENTS.md

## Project Overview

CCPeek is a Go CLI that indexes Claude Code data from `~/.claude` and Cursor data from `~/.cursor`, then serves a local web UI for browsing conversations, plans, todos, snapshots, file history, commands, and Claude-specific data sources (tasks, paste cache, usage data, memories). Indexed data is written to `$XDG_DATA_HOME/ccpeek/ccpeek.db` (defaults to `~/.local/share/ccpeek/ccpeek.db`).

## Commands

All tasks are managed via [just](https://github.com/casey/just). Install tools with `pnpm install`.

| Command                | Purpose                                                                                             |
| ---------------------- | --------------------------------------------------------------------------------------------------- |
| `just dev`             | Build CSS then run server with `--open`                                                             |
| `just build`           | Build CSS then compile Go binary to `cmd/ccpeek/ccpeek`                                             |
| `just css`             | Compile Tailwind CSS (input: `internal/web/src/app.css` -> output: `internal/web/static/style.css`) |
| `just css-watch`       | Watch mode for CSS                                                                                  |
| `just test`            | Run all tests (unit + e2e)                                                                          |
| `just test-unit`       | `go test -tags sqlite_fts5 ./...`                                                                   |
| `just test-e2e`        | Playwright e2e tests (builds CSS first, starts Go server on port 4322)                              |
| `just test-e2e-cursor` | Playwright mixed-source Cursor fixture lane (builds CSS first, starts Go server on port 4323)       |
| `just lint`            | oxlint with type checking                                                                           |
| `just format`          | prettier for JS/TS/HTML                                                                             |
