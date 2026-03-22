# Migration fixtures

These fixtures are focused SQLite databases used by `internal/store` migration tests.

They are intentionally small, but they model real historical schema shapes closely enough to exercise upgrade behavior for existing user data. The set also includes a deliberately damaged but versioned fixture to exercise recovery of rebuildable derived tables.

Regenerate them with:

```sh
just regen-migration-fixtures
```

Check that committed fixtures are in sync with their SQL sources:

```sh
just check-migration-fixtures
```

Or run the scripts directly:

```sh
./scripts/regenerate-migration-fixtures.sh
./scripts/check-migration-fixtures.sh
```
