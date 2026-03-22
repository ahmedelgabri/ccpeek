# Migration fixtures

These fixtures are focused old-schema SQLite databases used by `internal/store` migration tests.

They are intentionally small, but they model real historical schema shapes closely enough to exercise upgrade behavior for existing user data.

Regenerate them with:

```sh
just regen-migration-fixtures
```

Or run the script directly:

```sh
./scripts/regenerate-migration-fixtures.sh
```
