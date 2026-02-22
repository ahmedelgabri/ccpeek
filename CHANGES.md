# CCPeak

Go CLI that indexes `~/.claude` and serves a web UI for browsing conversations, plans, todos, file history, and shell snapshots.

## Architecture

- `cmd/ccpeak/` -- CLI entry point (flags: `--port`, `--skip-index`, `--index-only`, `--open`, `--claude-dir`, `--data-dir`)
- `internal/index/` -- reads `~/.claude`, writes structured data to `$TMPDIR/.ccpeak`
- `internal/model/` -- shared data types
- `internal/server/` -- HTTP server and routing
- `internal/web/` -- HTML templates and static assets

## Usage

```bash
go run ./cmd/ccpeak              # index + serve on :3000
go run ./cmd/ccpeak --open       # same, opens browser
go run ./cmd/ccpeak --index-only # index and exit
go run ./cmd/ccpeak --skip-index # serve existing data
```

## Tests

```bash
go test ./...                                              # unit + integration
pnpm exec playwright test --config playwright-go.config.ts # e2e
```

## History

Originally an Astro 5 static site, rewritten as a Go CLI to remove the Node build step and simplify deployment.
