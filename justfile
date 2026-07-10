css_input := "internal/web/src/app.css"
css_output := "internal/web/static/style.css"
binary := "cmd/ccpeek/ccpeek"

# Default: list available recipes
default:
    @just --list

css:
    pnpm exec tailwindcss --input {{css_input}} --output {{css_output}} --minify

css-watch:
    pnpm exec tailwindcss --input {{css_input}} --output {{css_output}} --watch

# Build the v2 SPA into internal/webui/dist (embedded via go:embed).
# .gitkeep is recreated because vite's emptyOutDir wipes it, and a fresh
# clone needs at least one tracked file for the go:embed pattern.
ui:
    pnpm -C ui install --frozen-lockfile
    pnpm -C ui exec tsc --noEmit
    pnpm -C ui exec vite build
    touch internal/webui/dist/.gitkeep

# Vite dev server with HMR, proxying /api to a running ccpeek server
ui-dev:
    pnpm -C ui exec vite

build: css ui
    CGO_ENABLED=1 go build -tags sqlite_fts5 -o {{binary}} ./cmd/ccpeek/

dev: css ui
    CGO_ENABLED=1 go run -tags sqlite_fts5 ./cmd/ccpeek --open --watch

vet:
    CGO_ENABLED=1 go vet -tags sqlite_fts5 ./...

staticcheck:
    staticcheck -tags sqlite_fts5 ./...

govulncheck:
    govulncheck -tags sqlite_fts5 ./...

lint:
    pnpm exec oxlint --type-aware --type-check

format:
    nix --extra-experimental-features 'nix-command flakes' fmt

format-check:
    nix --extra-experimental-features 'nix-command flakes' fmt -- --fail-on-change

test-unit: css
    CGO_ENABLED=1 go test -tags sqlite_fts5 ./...

test-race: css
    CGO_ENABLED=1 go test -race -tags sqlite_fts5 ./...

test-e2e: css ui
    pnpm exec playwright test --config=playwright-go.config.ts

test: test-unit test-e2e

regen-migration-fixtures:
    ./scripts/regenerate-migration-fixtures.sh

check-migration-fixtures:
    ./scripts/check-migration-fixtures.sh

clean:
    rm -f {{binary}} {{css_output}}
