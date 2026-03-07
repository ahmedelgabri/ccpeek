# Feature: Tasks, Paste Cache, and Usage Data Support

## Summary

Added support for three new `~/.claude` data sources: tasks, paste-cache, and usage-data. Each gets full indexing, storage, list/detail web views, and navigation integration.

## Data Sources

### Tasks (`~/.claude/tasks/`)

- UUID-named directories, each containing numbered JSON files (`1.json`, `2.json`, ...)
- Each task item has: id, subject, description, status, dependency tracking (`blocks`/`blockedBy`)
- Empty directories (only `.lock`/`.highwatermark`) are skipped during indexing
- Linked to sessions via directory UUID matching session ID

### Paste Cache (`~/.claude/paste-cache/`)

- Hex-named `.txt` files containing clipboard paste content
- Full content stored in DB and viewable on detail pages
- List view shows filename, size, and truncated preview

### Usage Data (`~/.claude/usage-data/`)

- `facets/*.json`: Per-session analysis with outcome, helpfulness, friction, goals, satisfaction
- `report.html`: Self-contained HTML report served via iframe
- Facets linked to sessions via `session_id` field, enabling project/session cross-references

## Changes

### Model (`internal/model/model.go`)

- `TaskGroupEntry`, `TaskItem`, `PasteCacheEntry`, `UsageFacetEntry`

### Schema (`internal/store/schema.go`)

- Tables: `task_groups`, `task_items`, `paste_cache`, `usage_facets`, `usage_report`
- Schema version bumped to 3

### Indexers (`internal/index/`)

- `tasks.go`: Reads task directories, parses numbered JSON, links to sessions
- `pastecache.go`: Reads `.txt` files
- `usagedata.go`: Reads facet JSON files and `report.html`

### Store (`internal/store/`)

- Write: `InsertTaskGroup`, `InsertPasteCache`, `InsertUsageFacet`, `InsertUsageReport`
- Read: `ListTaskGroups`, `GetTaskGroup`, `ListPasteCache`, `GetPasteCache`, `ListUsageFacets`, `GetUsageFacet`, `GetUsageReport`
- `GetStats` updated with 3 new counts

### Server

- 8 new routes: tasks list/detail, paste-cache list/detail, usage-data list/detail/report/raw
- Template helpers: `outcomeColor`, `helpfulnessColor`, `humanize`
- 7 new templates

### UI

- 3 new sidebar nav entries (Tasks, Paste Cache, Usage Data)
- 3 new dashboard stat cards
- Usage data list has "View Report" button linking to iframe page
- Task detail shows dependency graph (blocks/blockedBy)
- Usage facet detail shows outcome, helpfulness, goal categories, satisfaction, friction

### Tests

- Testdata fixtures for all 3 data sources
- Index tests: counts, linking, content verification
- Handler tests: list, detail, 404 for each data source + report iframe
