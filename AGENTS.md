# AGENTS.md

## Project Overview

CCPeek is a Go CLI that indexes Claude Code data from `~/.claude` and serves a local web UI for browsing conversations, plans, todos, tasks, shell snapshots, file history, paste cache, and usage data. Indexed data is written to `$XDG_DATA_HOME/ccpeek/ccpeek.db` (defaults to `~/.local/share/ccpeek/ccpeek.db`).

## Commands

All tasks are managed via [just](https://github.com/casey/just). Install tools with `pnpm install`.

| Command          | Purpose                                                                                             |
| ---------------- | --------------------------------------------------------------------------------------------------- |
| `just dev`       | Build CSS then run server with `--open`                                                             |
| `just build`     | Build CSS then compile Go binary to `cmd/ccpeek/ccpeek`                                             |
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
cmd/ccpeek/main.go       Entry point: delegates to internal/cmd
internal/
  cmd/
    root.go                 Cobra root command with flags and server logic
    man.go                  Hidden subcommand for man page generation
  index/                    Reads ~/.claude, writes ~/.local/share/ccpeek/ccpeek.db
    index.go                Orchestrator: Run(rebuild), RunIncremental(), Prune(), hash helpers
    incremental.go          Filtered variants of sub-indexers for incremental mode
    projects.go             Indexes projects + sessions from JSONL conversation files
    plans.go, todos.go,     Each indexes one entity type
    snapshots.go,
    filehistory.go
    tasks.go                Indexes ~/.claude/tasks/ (task groups with dependency tracking)
    pastecache.go           Indexes ~/.claude/paste-cache/ (clipboard paste content)
    usagedata.go            Indexes ~/.claude/usage-data/facets/ + report.html
    history.go              Parses history.jsonl timeline entries
    resolve.go              Cross-links entities (todos<->sessions, file history<->sessions)
    jsonl.go                Generic JSONL line reader (10MB buffer for large messages)
  model/                    All data types (IndexData, PlanEntry, ConversationMessage, etc.)
  server/
    server.go               ListenAndServe, route registration, request logger middleware
    handlers.go             HTTP handlers for all routes
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
- **Indexing pipeline**: `index.Run(rebuild bool)` performs indexing. By default it does incremental indexing: each source file is SHA-256 hashed and compared against stored hashes, only changed files are re-indexed (delete old rows, insert new). `--rebuild` forces a full drop-and-rebuild. `--prune` removes DB rows whose source files no longer exist on disk. Data from deleted source files is preserved by default. Each entity row stores a `source_path` column linking it back to its source file.
- **Schema migrations**: Sequential versioned migrations in `store/schema.go`. `initialSchema` is the baseline, `migrations` slice holds `func(tx)` for each version bump (e.g. v4→v5 added `source_path`/`content_hash` columns). `store.migrate()` applies pending migrations on startup.
- **Testing**: Handler tests use `httptest` with `NewHandler()` against real testdata fixtures. E2e tests use Playwright against a live Go server with `--skip-index`.
- **CSS**: Tailwind v4 with `@import 'tailwindcss'` and `@source "../templates"` directive. Custom theme colors (`surface`, `surface-card`, etc.) defined in `@theme` block. The compiled `static/style.css` is gitignored; run `just css` to generate it.
- **List items**: Use class `list-row` (not `list-item`, which collides with Tailwind's `display: list-item` utility).
- **Route pattern**: Go 1.22+ enhanced ServeMux with `GET /path/{param}/{$}` syntax. Trailing `{$}` enforces exact match.
