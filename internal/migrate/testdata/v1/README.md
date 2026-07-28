# Historical v1 databases

Real ccpeek v1 databases, one per schema vintage a v1 install could leave on disk (v4 through v14; the final release shape, v15, is modeled directly by the builders in `migrate_test.go`). Each `.db` is generated from its `.sql` seed, kept alongside as the readable source of truth.

The corpus is restored from v1's migration-test fixtures (`internal/store/testdata/migrations` on `main`). There it exercised the in-place upgrade chain; here it proves `ImportV1` reads every vintage: absent tables skip, absent columns narrow the SELECT, and nothing errors or silently drops data. `TestImportV1HistoricalFixtures` pins per-fixture row expectations derived from the seeds.

To regenerate a fixture after editing its seed:

```sh
rm x.db && sqlite3 x.db < x.sql
```
