# Claude History Browser

Static Astro app for browsing `~/.claude` contents.

## What was built

### Pre-indexer (`scripts/build-index.ts`)

- Reads `~/.claude` and outputs structured data to `src/data/` (gitignored)
- Parses JSONL conversation files into JSON, filtering to user/assistant/system messages only
- Copies plans (Markdown), shell snapshots (.sh), and todos (JSON)
- Indexes file-history with inline content
- Generates `index.json` with metadata and counts for all types

### Pages

- **Dashboard** (`/`): Type cards with counts, recent conversation history
- **Projects** (`/projects/`): Searchable project list, per-project session list with search, full conversation viewer with pagination
- **Plans** (`/plans/`): Searchable list, rendered markdown via react-markdown
- **Shell Snapshots** (`/shell-snapshots/`): List with timestamps, Shiki-highlighted code
- **Todos** (`/todos/`): List with status badges, rendered task items
- **File History** (`/file-history/`): Conversation list, expandable versioned file viewer

### React Islands

- `ConversationViewer`: Paginated message display with collapsible tool calls
- `JsonViewer`: Recursive collapsible JSON tree
- `MarkdownRenderer`: react-markdown with GFM
- `SearchInput` + type-specific wrappers: Client-side multi-term filtering
- `TodoList`: Status-badged task items

### Tech Stack

- Astro 5 (static output), React 19, Tailwind CSS v4, Shiki, pnpm
- Vitest (24 unit tests), Playwright (12 e2e tests)

## How to use

```bash
pnpm install
pnpm run index        # index ~/.claude -> src/data/
pnpm run dev          # dev server at localhost:4321
pnpm run build        # static build to dist/
pnpm test             # unit tests
pnpm run test:e2e     # e2e tests (needs build first)
```

## Stats

- 946 static pages generated
- Build time: ~5 seconds
- Index time: ~1 second
