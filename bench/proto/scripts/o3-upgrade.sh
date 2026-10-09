#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
proto_dir=$(CDPATH= cd -- "$script_dir/.." && pwd)
# shellcheck source=lib/common.sh
source "$script_dir/lib/common.sh"

usage() {
  cat <<'EOF'
Usage: o3-upgrade.sh

Build v1 from the current prototype, load its fixture into a fresh supermetry_o3_*
schema, then build a temporary v2 with one additional forward migration.
Run the upgrade with one command (`proto-v2 migrate`) and prove v1 data survives.

Environment: SUPERMETRY_TEST_DSN (base connection; isolated schemas are used)
EOF
}
case "${1:-}" in -h|--help) usage; exit 0 ;; '') ;; *) ops_die "unknown argument: $1" ;; esac

for cmd in go psql mktemp cp; do ops_require "$cmd"; done
ops_check_admin

tmp=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-o3.XXXXXX")
db="supermetry_o3_${UID}_$$"
cleanup() {
  ops_drop_db "$db" >/dev/null 2>&1 || true
  rm -rf -- "$tmp"
}
trap cleanup EXIT INT TERM

v1="$tmp/proto-v1"
v2="$tmp/proto-v2"
(cd "$proto_dir" && go build -buildvcs=false -trimpath -o "$v1" .)
ops_create_db "$db"
dsn=$(ops_dsn "$db")

SUPERMETRY_DSN="$dsn" "$v1" load --sites "$proto_dir/testdata/fixture_sites.json" \
  < "$proto_dir/testdata/fixture.ndjson" >/dev/null
before_events=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command 'SELECT count(*) FROM events')
before_sessions=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command 'SELECT count(*) FROM sessions')
before_fingerprint=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command "SELECT md5(string_agg(site_id || ':' || path || ':' || name, ',' ORDER BY ts, site_id, path, name)) FROM events")
v1_version=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command 'SELECT max(version) FROM schema_migrations')
[[ "$v1_version" =~ ^[0-9]+$ ]] || ops_die 'v1 has no migration version'

mkdir -p "$tmp/v2src"
cp -a "$proto_dir/." "$tmp/v2src/"
cat > "$tmp/v2src/internal/store/zz_o3_upgrade.go" <<'GO'
package store

func init() {
	migrations = append(migrations, `
CREATE TABLE upgrade_probe (
  id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  upgraded_at timestamptz NOT NULL DEFAULT now(),
  note        text NOT NULL DEFAULT 'o3-forward-migration'
);
`)
}
GO
(cd "$tmp/v2src" && go build -buildvcs=false -trimpath -o "$v2" .)

# This is the complete operator action being tested: one upgrade command.
SUPERMETRY_DSN="$dsn" "$v2" migrate
SUPERMETRY_DSN="$dsn" "$v2" migrate # idempotence: restart/retry applies nothing twice

v2_version=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command 'SELECT max(version) FROM schema_migrations')
after_events=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command 'SELECT count(*) FROM events')
after_sessions=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command 'SELECT count(*) FROM sessions')
after_fingerprint=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command "SELECT md5(string_agg(site_id || ':' || path || ':' || name, ',' ORDER BY ts, site_id, path, name)) FROM events")
probe=$(psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align --dbname "$dsn" \
  --command "SELECT count(*) FROM pg_class WHERE oid = 'upgrade_probe'::regclass")

((v2_version == v1_version + 1)) || ops_die "expected migration $((v1_version + 1)), got $v2_version"
[[ "$after_events" == "$before_events" ]] || ops_die 'event count changed during upgrade'
[[ "$after_sessions" == "$before_sessions" ]] || ops_die 'session count changed during upgrade'
[[ "$after_fingerprint" == "$before_fingerprint" ]] || ops_die 'event data changed during upgrade'
[[ "$probe" == 1 ]] || ops_die 'v2 migration object is absent'

printf 'O3 PASS: one-command upgrade migration %s -> %s; %s events and %s sessions survived\n' \
  "$v1_version" "$v2_version" "$after_events" "$after_sessions"
