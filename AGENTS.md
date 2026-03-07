# AGENTS.md

## Project Overview

CCPeek is a Go CLI that indexes Claude Code data from `~/.claude` and serves a local web UI for browsing conversations, plans, todos, shell snapshots, and file history. Indexed data is written to `$XDG_DATA_HOME/ccpeek/ccpeek.db` (defaults to `~/.local/share/ccpeek/ccpeek.db`).

## Commands

All tasks are managed via [just](https://github.com/casey/just). Install tools with `cd web && pnpm install`.

| Command            | Purpose                                                                          |
| ------------------ | -------------------------------------------------------------------------------- |
| `just dev`         | Build web assets then run server with `--open`                                   |
| `just build`       | Build web assets then compile Go binary to `cmd/ccpeek/ccpeek`                   |
| `just web-build`   | Build TypeScript + Tailwind CSS into `internal/web/dist/`                        |
| `just css-watch`   | Watch mode for CSS                                                               |
| `just test`        | Run all tests (unit + e2e)                                                       |
| `just test-unit`   | `go test ./...`                                                                  |
| `just test-e2e`    | Playwright e2e tests (builds web first, starts Go server on port 4322)           |
| `just lint`        | oxlint with type checking                                                        |
| `just typecheck`   | TypeScript type checking via `tsc --noEmit`                                      |
| `just format`      | prettier for TS/HTML                                                             |

Run a single Go test: `go test ./internal/server/ -run TestDashboard`

## Architecture

```
web/                         Web source (TypeScript + CSS)
  package.json               Node dependencies and build scripts
  tsconfig.json              TypeScript config (noEmit, IDE + type checking only)
  build.mjs                  esbuild + Tailwind build script
  src/
    app.css                  Tailwind v4 source CSS with custom dark theme
    app.ts                   Entry point importing all modules
    *.ts                     TypeScript modules (search, clipboard, heatmap, etc.)
  static/
    favicon.svg              Static assets copied to dist/
  playwright-go.config.ts    Playwright e2e test config
cmd/ccpeek/main.go           Entry point: delegates to internal/cmd
internal/
  cmd/
    root.go                  Cobra root command with flags and server logic
    man.go                   Hidden subcommand for man page generation
  index/                     Reads ~/.claude, writes ~/.local/share/ccpeek/ccpeek.db
    index.go                 Orchestrator: Run() calls each sub-indexer
    projects.go              Indexes projects + sessions from JSONL conversation files
    plans.go, todos.go,      Each indexes one entity type
    snapshots.go,
    filehistory.go
    history.go               Parses history.jsonl timeline entries
    resolve.go               Cross-links entities (todos<->sessions, file history<->sessions)
    jsonl.go                 Generic JSONL line reader (10MB buffer for large messages)
  model/                     All data types (IndexData, PlanEntry, ConversationMessage, etc.)
  server/
    server.go                ListenAndServe, route registration, request logger middleware
    handlers.go              HTTP handlers for all 12 routes
    render.go                Template loading (clones base template per page), markdown/code rendering
    helpers.go               Template functions: formatBytes, truncate, statusColor, encodeProjectDir, etc.
  web/
    embed.go                 //go:embed for templates/ and dist/
    dist/                    Build output (gitignored): app.js, shiki.js, style.css, favicon.svg
    templates/               Go html/template files
      layout.html            Base layout (sidebar + main content area)
      partials/              nav.html, pagination.html, message.html
      dashboard.html         Home page with stat cards + recent conversations
      *_list.html            List pages for each entity type
      *_detail.html          Detail pages
tests/e2e-go/                Playwright specs (navigation, content browsing)
testdata/                    Fixture data mimicking ~/.claude structure for unit tests
```

## Key Patterns

- **Templates**: Each page defines `{{template "content"}}`. The base `layout.html` is cloned per page template to avoid definition collisions. Template functions are registered in `render.go`.
- **Embedded assets**: Templates and built assets (`dist/`) are embedded via `//go:embed` in `web/embed.go`. Changes take effect on rebuild, not at runtime.
- **Web build**: `web/build.mjs` uses esbuild to bundle TypeScript into `internal/web/dist/`, runs Tailwind CSS, and copies static assets. Two JS entry points: `app.js` (all modules bundled) and `shiki.js` (separate, loads CDN dependency).
- **Indexing pipeline**: `index.Run()` reads source data, each sub-indexer handles one entity type, then `resolveRelationships()` cross-links entities using UUID extraction from filenames.
- **Testing**: Handler tests use `httptest` with `NewHandler()` against real testdata fixtures. E2e tests use Playwright against a live Go server with `--skip-index`.
- **CSS**: Tailwind v4 with `@import 'tailwindcss'` and `@source "../../internal/web/templates"` directive. Custom theme colors (`surface`, `surface-card`, etc.) defined in `@theme` block. The compiled CSS is output to `internal/web/dist/style.css` (gitignored); run `just web-build` to generate it.
- **List items**: Use class `list-row` (not `list-item`, which collides with Tailwind's `display: list-item` utility).
- **Route pattern**: Go 1.22+ enhanced ServeMux with `GET /path/{param}/{$}` syntax. Trailing `{$}` enforces exact match.
