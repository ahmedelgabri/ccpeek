css_input := "internal/web/src/app.css"
css_output := "internal/web/static/style.css"
binary := "cmd/ccpeek/ccpeek"

css:
    pnpm exec tailwindcss --input {{css_input}} --output {{css_output}} --minify

css-watch:
    pnpm exec tailwindcss --input {{css_input}} --output {{css_output}} --watch

build: css
    go build -o {{binary}} ./cmd/ccpeek/

dev: css
    go run ./cmd/ccpeek --open

lint:
    pnpm exec oxlint --type-aware --type-check

format:
    prettier --write "**/*.{js,ts,html}"

format-check:
    prettier --check "**/*.{js,ts,html}"

test-unit: css
    go test ./...

test-e2e: css
    pnpm exec playwright test --config=playwright-go.config.ts

test: test-unit test-e2e

clean:
    rm -f {{binary}} {{css_output}}
