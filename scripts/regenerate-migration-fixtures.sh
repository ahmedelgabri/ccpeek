#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
fixture_dir="$repo_root/internal/store/testdata/migrations"

rm -f "$fixture_dir"/*.db

for sql in \
  v4-earliest-supported.sql \
  v5-source-path-and-plan-data.sql \
  v7-legacy-sessions-and-scan-findings.sql \
  v8-session-uniqueness.sql \
  v10-derived-data.sql \
  v13-delete-actions.sql

do
  db=${sql%.sql}.db
  sqlite3 "$fixture_dir/$db" < "$fixture_dir/$sql"
done
