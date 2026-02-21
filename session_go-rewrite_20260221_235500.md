# Go CLI Rewrite - Session Summary

## What was done

Rewrote the Claude History Browser from an Astro/React static site to a single Go binary (`claude-history`) that indexes `~/.claude` data and serves a local web UI.

## Architecture

- **Single binary** with CLI flags: `--index-only`, `--skip-index`, `--port`, `--claude-dir`, `--data-dir`, `--open`
- **Indexer** reads `~/.claude/` and writes structured data to `~/.claude-history/`
- **Server** loads the index at startup and serves HTML via Go `html/template`
- **Frontend** is server-rendered HTML + ~30 lines of vanilla JS for client-side search
- **Two external deps**: goldmark (markdown), chroma (syntax highlighting)

## File structure

```
main.go                           # CLI entry
internal/
  model/model.go                  # All data types
  index/                          # Indexer (plans, snapshots, todos, projects, file-history, history)
  server/                         # HTTP server, handlers, helpers, template rendering
  web/
    embed.go                      # //go:embed
    templates/                    # All HTML templates
    static/                       # CSS + JS
testdata/                         # Test fixtures
```

## Key decisions

- **Per-page template cloning**: Go's `template.ParseFS` with multiple files defining `{{define "content"}}` causes collisions. Fixed by parsing layout+partials as a base, then cloning for each page template.
- **`trimSuffix` pipe order**: Go template pipes pass the piped value as the last argument, so `trimSuffix` wrapper takes `(suffix, s)` not `(s, suffix)`.
- **`json.RawMessage` for message content**: Handles the union type (string or array of content blocks) cleanly.

## Test coverage

- **Unit tests** (20 tests): model methods, indexer, format helpers
- **Integration tests** (14 tests): all HTTP handlers via `httptest`
- **E2e tests** (12 tests): Playwright against running Go server

## Verification

- `go vet ./...` clean
- `go test ./...` all pass
- `pnpm exec playwright test --config playwright-go.config.ts` all 12 pass
- Tested against real `~/.claude` data: 27 plans, 71 snapshots, 13 todos, 17 projects (602 sessions), 221 file history entries, 1590 history entries
