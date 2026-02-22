# Claude History Browser

Go CLI that indexes `~/.claude` and serves a web UI for browsing conversations, plans, todos, file history, and shell snapshots.

## Architecture

- `cmd/claude-history/` -- CLI entry point (flags: `--port`, `--skip-index`, `--index-only`, `--open`, `--claude-dir`, `--data-dir`)
- `internal/index/` -- reads `~/.claude`, writes structured data to `~/.claude-history`
- `internal/model/` -- shared data types
- `internal/server/` -- HTTP server and routing
- `internal/web/` -- HTML templates and static assets

## Usage

```bash
go run ./cmd/claude-history              # index + serve on :3000
go run ./cmd/claude-history --open       # same, opens browser
go run ./cmd/claude-history --index-only # index and exit
go run ./cmd/claude-history --skip-index # serve existing data
```

## Tests

```bash
go test ./...                                              # unit + integration
pnpm exec playwright test --config playwright-go.config.ts # e2e
```

## History

Originally an Astro 5 static site, rewritten as a Go CLI to remove the Node build step and simplify deployment.
