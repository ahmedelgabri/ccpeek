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

# The withui tag selects the full-product variant: the SPA's presence
# is enforced at compile time (see internal/webui), so this recipe can
# never ship a UI-less binary. Plain `go build` yields the API-only
# variant instead.
build: ui
    go build -tags withui -o {{binary}} ./cmd/ccpeek/

dev: ui
    go run -tags withui ./cmd/ccpeek --open --watch

# The static checks run TWICE: untagged for the API-only variant that a
# plain `go build`/`go install` produces, and with withui for the variant
# every release path actually ships. Only the tagged pass compiles
# internal/webui/embed_withui.go at all, so without it a mistake behind
# the build tag reached the release job untouched by vet, staticcheck or
# the tests. The tagged pass needs the SPA built first — the embed
# pattern is what enforces its presence.
vet: ui
    go vet ./...
    go vet -tags withui ./...

staticcheck: ui
    staticcheck ./...
    staticcheck -tags withui ./...

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

# Unit tests likewise cover both variants: internal/webui's tests only
# exercise the API-only path untagged (Embedded() reports false), so the
# real embed — the SPA-serving handler users get — is covered only by the
# tagged pass.
test-unit: ui
    go test ./...
    go test -tags withui ./internal/webui/...

test-race:
    go test -race ./...

test-e2e: ui
    pnpm exec playwright test --config=playwright-go.config.ts

test: test-unit test-e2e

clean:
    rm -f {{binary}}
