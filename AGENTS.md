# AGENTS.md

## Project Overview

CCPeek is a Go CLI that indexes coding-agent history — Claude Code, Pi, Codex CLI, OpenCode, and Cursor — into one session-centric SQLite database and serves a local web UI (React SPA embedded in the binary) plus an agent-facing query surface (`ccpeek query`, `/api/v1`, `ccpeek mcp`). Indexed data is written to `$XDG_DATA_HOME/ccpeek/ccpeek2.db` (defaults to `~/.local/share/ccpeek/ccpeek2.db`); a legacy v1 `ccpeek.db` next to it is imported on first run and never modified. The server binds its port immediately and runs indexing in the background (`/api/v1/ready` flips to 200 when the first pass completes). See `docs/v2-plan.md` for the architecture.

## Commands

All tasks are managed via [just](https://github.com/casey/just). Install tools with `pnpm install`.

| Command          | Purpose                                                                       |
| ---------------- | ----------------------------------------------------------------------------- |
| `just dev`       | Build the UI then run the server with `--open --watch`                        |
| `just build`     | Build the UI then compile the Go binary to `cmd/ccpeek/ccpeek`                |
| `just ui`        | Build the SPA (`ui/` -> `internal/webui/dist`, embedded via go:embed)         |
| `just ui-dev`    | Vite dev server with HMR, proxying `/api` to a running ccpeek server          |
| `just test`      | Run all tests (unit + e2e)                                                    |
| `just test-unit` | `go test -tags sqlite_fts5 ./...`                                             |
| `just test-e2e`  | Playwright e2e tests (builds the UI first, starts the Go server on port 4322) |
| `just lint`      | oxlint with type checking                                                     |
| `just format`    | treefmt via `nix fmt` (gofumpt + prettier + alejandra)                        |
| `just vet`       | `go vet` with the right build tags                                            |

The e2e suite pins every agent root at `testdata/` (via env in `playwright-go.config.ts`) so it never ingests real agent data from the developer's machine.
