#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  exit 1
}

for name in o2-install.sh o3-upgrade.sh o4-backup.sh; do
  path="$script_dir/$name"
  [[ -f "$path" ]] || fail "$name is missing"
  [[ -x "$path" ]] || fail "$name is not executable"
  bash -n "$path"
  "$path" --help | grep -q '^Usage:' || fail "$name has no Usage help"
  grep -q 'panel_' "$path" || fail "$name does not name an isolated panel_* schema"
  grep -q 'ops_drop_db' "$path" || fail "$name does not clean up its schema"
done

grep -q 'DROP SCHEMA' "$script_dir/lib/common.sh" || fail 'shared cleanup does not drop schemas'

if grep -Eiq '(pg_ctl(cluster)?[[:space:]].*(restart|reload)|systemctl[[:space:]].*postgres|ALTER[[:space:]]+SYSTEM)' \
    "$script_dir"/o[234]-*.sh; then
  fail 'an operations script restarts or reconfigures PostgreSQL'
fi

grep -q 'pg_dump' "$script_dir/o4-backup.sh" || fail 'O4 does not use pg_dump'
grep -q 'pg_restore' "$script_dir/o4-backup.sh" || fail 'O4 does not use pg_restore'
grep -q 'proto.*verify' "$script_dir/o4-backup.sh" || fail 'O4 does not run proto verify'

printf 'PASS: operations script contract\n'
