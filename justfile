binary := "cmd/ccpeek/ccpeek"

# Default: list available recipes
default:
    @just --list

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

build: ui
    go build -o {{binary}} ./cmd/ccpeek/

dev: ui
    go run ./cmd/ccpeek --open --watch

vet:
    go vet ./...

staticcheck:
    staticcheck ./...

govulncheck:
    govulncheck ./...

# Type-aware linting covers ui/ too, whose deps live in its own package
# — install them so oxlint can resolve vite/react module types in CI.
lint:
    pnpm -C ui install --frozen-lockfile
    pnpm exec oxlint --type-aware --type-check

format:
    nix --extra-experimental-features 'nix-command flakes' fmt

format-check:
    nix --extra-experimental-features 'nix-command flakes' fmt -- --fail-on-change

test-unit:
    go test ./...

test-race:
    go test -race ./...

test-e2e: ui
    pnpm exec playwright test --config=playwright-go.config.ts

test: test-unit test-e2e

clean:
    rm -f {{binary}}
