# Migration fixtures

These fixtures are focused old-schema SQLite databases used by `internal/store` migration tests.

They are intentionally small, but they model real historical schema shapes closely enough to exercise upgrade behavior for existing user data.

Regenerate them with:

```sh
rm -f internal/store/testdata/migrations/*.db
sqlite3 internal/store/testdata/migrations/v4-earliest-supported.db < internal/store/testdata/migrations/v4-earliest-supported.sql
sqlite3 internal/store/testdata/migrations/v5-source-path-and-plan-data.db < internal/store/testdata/migrations/v5-source-path-and-plan-data.sql
sqlite3 internal/store/testdata/migrations/v7-legacy-sessions-and-scan-findings.db < internal/store/testdata/migrations/v7-legacy-sessions-and-scan-findings.sql
sqlite3 internal/store/testdata/migrations/v8-session-uniqueness.db < internal/store/testdata/migrations/v8-session-uniqueness.sql
sqlite3 internal/store/testdata/migrations/v10-derived-data.db < internal/store/testdata/migrations/v10-derived-data.sql
sqlite3 internal/store/testdata/migrations/v13-delete-actions.db < internal/store/testdata/migrations/v13-delete-actions.sql
```
