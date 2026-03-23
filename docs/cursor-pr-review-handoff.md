# Cursor PR Review Handoff (No Push / No PR Open)

This document captures the final review package for the Cursor support PR
candidate. It intentionally stops before any push or PR creation.

## 1) Baseline And Scope Lock

### Baseline facts (captured now)

- Branch: `cursor-upstream-integration`
- `HEAD`: `84adc3b2add61442d4962bdf943b921d63c46375`
- `origin/main`: `84adc3b2add61442d4962bdf943b921d63c46375`
- Commit divergence (`origin/main...HEAD`): `0 0`
- Working tree delta: `39 files changed, 1099 insertions(+), 244 deletions(-)`
- Working tree entries (tracked + untracked): `58`

### In-scope features

- First-class mixed-source support for Claude + Cursor across indexing/storage/UI.
- Cursor JSONL sessions, Cursor plans/todos, Cursor snapshots, and Cursor SQLite
  metadata/file-history extraction.
- Metadata-only session behavior contract (view/search/export).
- Operational controls for large Cursor SQLite datasets.
- Deterministic test coverage across indexers, handlers, migrations, and e2e.

### Explicit non-goals

- No push/open-PR action in this handoff.
- No unrelated architectural churn outside Cursor integration hardening.
- No force/rewrite git operations.

## 2) CI And Release Workflow Map

### CI (`.github/workflows/ci.yml`)

- `lint` job:
  - `nix develop --command pnpm install`
  - `nix develop --command just format-check`
  - `nix develop --command just vet`
  - `nix develop --command just lint`
- `test` job:
  - `pnpm install`
  - `just test-unit`
  - `npx playwright install --with-deps chromium`
  - `just test-e2e`
  - `just test-e2e-cursor` (added mixed-source fixture lane)
- `build` job:
  - `nix flake check`
  - `nix build`
  - `./result/bin/ccpeek --help`

### Release (`.github/workflows/release.yml`)

- Trigger: successful `CI` on `main`.
- Builds Linux + Darwin (amd64/arm64), `CGO_ENABLED=1`, `-tags sqlite_fts5`.
- Generates completions + man pages.
- Publishes GitHub release assets + checksums.
- Updates Homebrew formula (`desc` aligned to AI coding history wording).

## 3) Validation Matrix (Executed)

### CI-equivalent (or closest available locally)

| Gate                    | Command                                                             | Result  | Notes                                                             |
| ----------------------- | ------------------------------------------------------------------- | ------- | ----------------------------------------------------------------- |
| Node deps               | `pnpm install`                                                      | PASS    | lockfile up to date                                               |
| Go vet                  | `CGO_ENABLED=1 go vet -tags sqlite_fts5 ./...`                      | PASS    | CI equivalent                                                     |
| JS lint (closest local) | `pnpm dlx oxlint`                                                   | PASS    | `--type-aware --type-check` requires nix-provided tsgolint binary |
| Unit tests              | `go test -tags sqlite_fts5 ./...`                                   | PASS    | all packages green                                                |
| Browser deps            | `npx playwright install --with-deps chromium`                       | PASS    | installed                                                         |
| E2E deterministic lane  | `pnpm exec playwright test --config=playwright-go.config.ts`        | PASS    | 17 passed                                                         |
| E2E mixed-source lane   | `pnpm exec playwright test --config=playwright-go-cursor.config.ts` | PASS    | 2 passed                                                          |
| Build                   | `CGO_ENABLED=1 go build -tags sqlite_fts5 ./cmd/ccpeek`             | PASS    | binary builds                                                     |
| Nix flake check         | `nix flake check`                                                   | NOT RUN | `nix` not available locally                                       |
| Nix build               | `nix build`                                                         | NOT RUN | `nix` not available locally                                       |

### Additional top-tier gates

| Gate               | Command                                                                                             | Result | Notes                                                 |
| ------------------ | --------------------------------------------------------------------------------------------------- | ------ | ----------------------------------------------------- |
| Static analysis    | `$(go env GOPATH)/bin/staticcheck ./...`                                                            | PASS   | no findings                                           |
| Vulnerability scan | `GOTOOLCHAIN=go1.26.1 $(go env GOPATH)/bin/govulncheck ./...`                                       | PASS   | go1.26.1 removes stdlib false negatives from go1.26.0 |
| Race tests         | `CGO_ENABLED=1 go test -race -tags sqlite_fts5 ./internal/store ./internal/index ./internal/server` | PASS   | key subsystems green                                  |

## 4) Reviewer-Facing Commit Plan (Recommended)

When you are ready to commit, keep a single PR with this sequence:

1. `schema/model`: source + metadata columns, migration coverage.
2. `index-core`: mixed-source orchestration, source fingerprinting.
3. `cursor-sqlite-operability`: flags/options, size guardrails, mode toggles.
4. `server-ui-contract`: metadata-only handling, source badges, copy alignment.
5. `tests`: sqlite fixtures, incremental/prune, snapshot/file-history, handler contracts, e2e cursor lane.
6. `docs-and-ci`: README/runbook updates, CI mixed-source e2e lane, handoff docs.

## 5) Draft PR Narrative (Prepared, Not Opened)

### Suggested title

`feat: add first-class Cursor support across indexing, storage, and UI`

### Suggested body

```md
## Summary

- Add first-class Cursor support alongside Claude across indexing, store schema, and UI rendering.
- Support Cursor JSONL, plans/todos, snapshots, and SQLite metadata/file-history extraction with explicit degraded-state handling.
- Harden operations for large Cursor SQLite stores via index options and incremental metadata fingerprinting.

## Behavior Contracts

- Metadata-only Cursor sessions are explicitly labeled and intentionally limited for transcript-body search/export.
- Mixed-source lists/pages show source badges and keep sorting/filter behavior deterministic.
- Cursor SQLite indexing is operator-controlled (`--cursor-sqlite`, `--cursor-sqlite-max-db-size-mb`).

## Validation

- go test (sqlite_fts5), go vet, staticcheck, race tests: pass.
- Playwright deterministic lane + fixture-based mixed-source lane: pass.
- govulncheck: pass with patched toolchain (`GOTOOLCHAIN=go1.26.1`).

## Risks & Mitigations

- Cursor private storage format drift: covered by defensive extraction + fixture tests.
- Large SQLite stores: mitigated via size guardrails and metadata fingerprinting.
- Metadata-only ambiguity: mitigated by explicit UI/export messaging and tests.
```

## 6) Risk Register (Open / Residual)

- Upstream draft PR #3 overlap risk remains; Cursor logic is kept isolated where practical.
- Local machine lacks `nix`, so `nix flake check` / `nix build` must still be confirmed in CI.
- govulncheck is sensitive to local toolchain version; use `GOTOOLCHAIN=go1.26.1` for accurate baseline.

## 7) Final Handoff State

- No push performed.
- No PR created.
- Branch remains at `HEAD == origin/main` with working-tree changes ready for your review.
