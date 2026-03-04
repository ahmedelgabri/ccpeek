# SQLite-Enabled Features Implementation

Branch: `feat/sqlite-features` (11 commits on top of `main`)

## Features Implemented

### 1. FTS5 Snippet Highlighting

- Changed FTS5 `snippet()` markers to `<mark>` tags for search result highlighting
- Fixed pre-existing bug: FTS5 table was configured as external content but the content column didn't exist in the messages table, causing `snippet()` to always fail
- Changed FTS5 table to standalone mode so snippet extraction works correctly
- Template renders snippets with `safeHTML` to allow mark tags

### 2. Server-Side Sort/Filter on Sessions List

- Added `SessionFilter` struct with Sort, Branch, From, To fields
- Added `ListSessionsFiltered()` store method with dynamic SQL query building
- Added `GetProjectID()` and `ListBranches()` helper queries
- Sessions list now supports `?sort=`, `?branch=`, `?from=`, `?to=` query params
- Template has branch dropdown and sort select controls

### 3. Incremental Indexing (mtime-based)

- Bumped schema version to 2
- Added `source_files` table tracking file paths and modification times
- `RunIncremental()` checks all source file mtimes before deciding to re-index
- Watch loop now uses `RunIncremental()` to skip unchanged data
- Detects both modified and deleted files

### 4. Date Range Filtering on Sessions

- Added date input fields (`from`, `to`) to sessions list form
- Leverages the `SessionFilter` struct from feature 2
- Filter values are preserved in the form on page reload

### 5. Tool Usage Analytics on Dashboard

- Added `GetToolUsageStats()` store method that aggregates `tool_use_counts` JSON across all sessions
- Dashboard shows top 15 tools as horizontal CSS bar chart with percentage widths
- Returns `ToolUsageStat` structs with Name, Count, Percent

### 6. Per-Project Stats

- Added `GetProjectStats()` returning session count, total messages, tokens, and date range
- Stats summary card displayed at top of sessions list page

### 7. Sorting Options on List Pages

- Added "Most tool calls" sort option using `json_each()` SQL function
- Aggregates tool usage counts per session for sorting

### 8. Advanced Search Filters

- Added `SearchFilter` struct with Project, Role, From, To fields
- Extended `Search()` to combine FTS5 MATCH with WHERE clauses
- Added `ListProjectNames()` for filter dropdown
- Search page has project dropdown, role dropdown, and date inputs

### 9. Token Usage Dashboard Chart

- Added `GetTokenTimeline()` store method: daily token aggregation via `GROUP BY date(created_at)`
- SVG bar chart rendered in `token-chart.js` with hover titles
- Shows date range labels below the chart

### 10. Tool Timeline Visualization

- Added `GetToolTimeline()` store method: extracts tool_use blocks with timestamps from message content JSON
- Swim-lane SVG timeline on the conversation tools tab
- Color-coded lanes per tool type with legend
- Both `token-chart.js` and `tool-timeline.js` wrapped in IIFEs to avoid scope collisions

### 11. Session Comparison Improvements

- Added `formatDuration()` and `percentDiff()` helpers
- Session cards now show duration and total tool calls
- Summary card with percentage diff indicators (color-coded: green for increase, red for decrease)
- Tool comparison table includes horizontal bar visualization and percentage diff column

## Key Files Modified

| File                                             | Features                                             |
| ------------------------------------------------ | ---------------------------------------------------- |
| `internal/store/schema.go`                       | #1 (FTS5 fix), #3 (source_files table, version bump) |
| `internal/store/store.go`                        | #3 (source_files in Reset())                         |
| `internal/store/read.go`                         | #1-#11 (all query methods)                           |
| `internal/store/write.go`                        | #3 (SetSourceFileMtime)                              |
| `internal/index/index.go`                        | #3 (incremental indexing)                            |
| `internal/server/handlers.go`                    | #1-#11 (handler changes)                             |
| `internal/server/helpers.go`                     | #11 (formatDuration, percentDiff)                    |
| `internal/server/helpers_test.go`                | #11 (new helper tests)                               |
| `internal/server/handlers_test.go`               | #1-#11 (test additions)                              |
| `internal/web/templates/search.html`             | #1, #8                                               |
| `internal/web/templates/sessions_list.html`      | #2, #4, #6, #7                                       |
| `internal/web/templates/dashboard.html`          | #5, #9                                               |
| `internal/web/templates/conversation_tools.html` | #10                                                  |
| `internal/web/templates/session_compare.html`    | #11                                                  |
| `internal/web/static/token-chart.js`             | #9 (new file)                                        |
| `internal/web/static/tool-timeline.js`           | #10 (new file)                                       |
| `internal/web/static/app.js`                     | #9, #10 (imports)                                    |

## Notable Fixes During Implementation

- **FTS5 external content bug**: The `messages_fts` table was defined with `content=messages` but the messages table lacked the expected `text_content` column. Changed to standalone FTS5 table.
- **oxlint scope collision**: Multiple JS files declaring `const container` at module scope triggered TS2451. Fixed by wrapping each in an IIFE.
- **oxlint restrict-template-expressions**: Values from `JSON.parse()` typed as `unknown` in template literals. Fixed with `String()` coercion.
