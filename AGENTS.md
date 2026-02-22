# AGENTS.md

## Project Overview

CCPeak is a Go CLI that indexes Claude Code data from `~/.claude` and serves a local web UI for browsing conversations, plans, todos, shell snapshots, and file history. Indexed data is written to `$TMPDIR/.ccpeak/index.json`.

## Commands

All tasks are managed via [just](https://github.com/casey/just). Install tools with `pnpm install`.

| Command          | Purpose                                                                                             |
| ---------------- | --------------------------------------------------------------------------------------------------- |
| `just dev`       | Build CSS then run server with `--open`                                                             |
| `just build`     | Build CSS then compile Go binary to `cmd/ccpeak/ccpeak`                                       |
| `just css`       | Compile Tailwind CSS (input: `internal/web/src/app.css` -> output: `internal/web/static/style.css`) |
| `just css-watch` | Watch mode for CSS                                                                                  |
| `just test`      | Run all tests (unit + e2e)                                                                          |
| `just test-unit` | `go test ./...`                                                                                     |
| `just test-e2e`  | Playwright e2e tests (builds CSS first, starts Go server on port 4322)                              |
| `just lint`      | oxlint with type checking                                                                           |
| `just format`    | prettier for JS/TS/HTML                                                                             |

Run a single Go test: `go test ./internal/server/ -run TestDashboard`

## Architecture

```
cmd/ccpeak/main.go       Entry point: delegates to internal/cmd
internal/
  cmd/
    root.go                 Cobra root command with flags and server logic
    man.go                  Hidden subcommand for man page generation
  index/                    Reads ~/.claude, writes $TMPDIR/.ccpeak/index.json
    index.go                Orchestrator: Run() calls each sub-indexer
    projects.go             Indexes projects + sessions from JSONL conversation files
    plans.go, todos.go,     Each indexes one entity type
    snapshots.go,
    filehistory.go
    history.go              Parses history.jsonl timeline entries
    resolve.go              Cross-links entities (todos<->sessions, file history<->sessions)
    jsonl.go                Generic JSONL line reader (10MB buffer for large messages)
  model/                    All data types (IndexData, PlanEntry, ConversationMessage, etc.)
  server/
    server.go               ListenAndServe, route registration, request logger middleware
    handlers.go             HTTP handlers for all 12 routes
    render.go               Template loading (clones base template per page), markdown/code rendering
    helpers.go              Template functions: formatBytes, truncate, statusColor, encodeProjectDir, etc.
  web/
    embed.go                //go:embed for templates/ and static/
    src/app.css             Tailwind v4 source CSS with custom dark theme
    static/style.css        Compiled CSS output (gitignored, rebuilt by css:build)
    static/app.js           Client-side search/filter for list pages
    templates/              Go html/template files
      layout.html           Base layout (sidebar + main content area)
      partials/             nav.html, pagination.html, message.html
      dashboard.html        Home page with stat cards + recent conversations
      *_list.html           List pages for each entity type
      *_detail.html         Detail pages
tests/e2e-go/               Playwright specs (navigation, content browsing)
testdata/                   Fixture data mimicking ~/.claude structure for unit tests
```

## Key Patterns

- **Templates**: Each page defines `{{template "content"}}`. The base `layout.html` is cloned per page template to avoid definition collisions. Template functions are registered in `render.go`.
- **Embedded assets**: All templates and static files are embedded via `//go:embed` in `web/embed.go`. Changes to templates take effect on rebuild, not at runtime.
- **Indexing pipeline**: `index.Run()` reads source data, each sub-indexer handles one entity type, then `resolveRelationships()` cross-links entities using UUID extraction from filenames.
- **Testing**: Handler tests use `httptest` with `NewHandler()` against real testdata fixtures. E2e tests use Playwright against a live Go server with `--skip-index`.
- **CSS**: Tailwind v4 with `@import 'tailwindcss'` and `@source "../templates"` directive. Custom theme colors (`surface`, `surface-card`, etc.) defined in `@theme` block. The compiled `static/style.css` is gitignored; run `just css` to generate it.
- **List items**: Use class `list-row` (not `list-item`, which collides with Tailwind's `display: list-item` utility).
- **Route pattern**: Go 1.22+ enhanced ServeMux with `GET /path/{param}/{$}` syntax. Trailing `{$}` enforces exact match.
