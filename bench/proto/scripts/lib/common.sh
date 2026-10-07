#!/usr/bin/env bash

# Shared safety helpers for the O2-O4 benchmark scripts. Callers enable
# errexit/nounset/pipefail before sourcing this file.

ops_die() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

ops_require() {
  command -v "$1" >/dev/null 2>&1 || ops_die "required command not found: $1"
}

ops_validate_db_name() {
  case "$1" in
    panel|postgres|template0|template1|'') ops_die "refusing unsafe database name: ${1:-<empty>}" ;;
  esac
  [[ "$1" =~ ^panel_(o2|o3|o4)_[a-z0-9_]+$ ]] || ops_die "invalid isolated database name: $1"
}

ops_admin_psql() {
	psql -X --set=ON_ERROR_STOP=1 --dbname "$(ops_base_dsn)" "$@"
}

ops_check_admin() {
	ops_admin_psql --quiet --tuples-only --no-align --command "SELECT has_database_privilege(current_user, current_database(), 'CREATE')" |
		grep -qx t || ops_die 'PANEL_TEST_DSN role cannot create isolated schemas'
}

ops_create_db() {
  local db=$1
  ops_validate_db_name "$db"
  ops_admin_psql --quiet --set=db="$db" <<'SQL'
SELECT format('CREATE SCHEMA %I', :'db') \gexec
SQL
}

ops_drop_db() {
  local db=$1
  ops_validate_db_name "$db"
  ops_admin_psql --quiet --set=db="$db" <<'SQL'
SELECT format('DROP SCHEMA IF EXISTS %I CASCADE', :'db') \gexec
SQL
}

ops_base_dsn() {
	printf '%s\n' "${PANEL_TEST_DSN:-postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable}"
}

ops_dsn() {
  local db=$1 base
  ops_validate_db_name "$db"
  base=$(ops_base_dsn)
  if [[ "$base" == *\?* ]]; then
    printf '%s&options=-csearch_path%%3D%s\n' "$base" "$db"
  else
    printf '%s?options=-csearch_path%%3D%s\n' "$base" "$db"
  fi
}

ops_wait_http() {
  local url=$1 pid=$2 i
  for i in $(seq 1 200); do
    kill -0 "$pid" 2>/dev/null || return 1
    if curl --fail --silent --show-error --max-time 1 "$url/api/stats" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.05
  done
  return 1
}

ops_stop_process() {
  local pid=${1:-}
  [[ -n "$pid" ]] || return 0
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
}
