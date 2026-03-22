#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
fixture_dir="$repo_root/internal/store/testdata/migrations"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT INT TERM HUP

sha3sum_db() {
  printf '.sha3sum --schema\n' | sqlite3 "$1"
}

"$repo_root/scripts/regenerate-migration-fixtures.sh" "$tmp_dir"

status=0
for db in "$fixture_dir"/*.db
do
  name=$(basename "$db")
  committed_hash=$(sha3sum_db "$db")
  regenerated_hash=$(sha3sum_db "$tmp_dir/$name")
  if [ "$committed_hash" != "$regenerated_hash" ]; then
    printf 'fixture out of sync: %s\n' "$name" >&2
    status=1
  fi
done

exit "$status"
