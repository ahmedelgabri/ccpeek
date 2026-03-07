binary := "cmd/ccpeek/ccpeek"

web-build:
    cd web && pnpm run build

css-watch:
    cd web && pnpm exec tailwindcss --input src/app.css --output ../internal/web/dist/style.css --watch

build: web-build
    CGO_ENABLED=1 go build -tags sqlite_fts5 -o {{binary}} ./cmd/ccpeek/

dev: web-build
    CGO_ENABLED=1 go run -tags sqlite_fts5 ./cmd/ccpeek --open

lint:
    cd web && pnpm exec oxlint --type-aware --type-check

typecheck:
    cd web && pnpm run typecheck

format:
    cd web && pnpm exec prettier --write "src/**/*.ts" "../internal/web/templates/**/*.html"

format-check:
    cd web && pnpm exec prettier --check "src/**/*.ts" "../internal/web/templates/**/*.html"

test-unit: web-build
    CGO_ENABLED=1 go test -tags sqlite_fts5 ./...

test-e2e: web-build
    cd web && pnpm exec playwright test --config=playwright-go.config.ts

test: test-unit test-e2e

clean:
    rm -rf {{binary}} internal/web/dist
