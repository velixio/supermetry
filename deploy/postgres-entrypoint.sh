#!/bin/sh
# Starts the bundled PostgreSQL. When POSTGRES_PASSWORD is not set, take the role,
# password and database name from SUPERMETRY_DATABASE_URL
# (postgres://user:password@postgres:5432/dbname?...), so there is one place to
# configure them. The password may be percent-encoded, as in the URL.
set -eu

if [ -z "${POSTGRES_PASSWORD:-}" ] && [ -n "${SUPERMETRY_DATABASE_URL:-}" ]; then
  rest="${SUPERMETRY_DATABASE_URL#*://}"
  case "$rest" in
    *@*) ;;
    *) echo "SUPERMETRY_DATABASE_URL has no user:password@host part" >&2; exit 1 ;;
  esac
  credentials="${rest%%@*}"
  after="${rest#*@}"
  user="${credentials%%:*}"
  password="${credentials#*:}"
  database="${after#*/}"
  database="${database%%\?*}"
  if [ "$credentials" = "$user" ] || [ -z "$user" ] || [ -z "$password" ] || [ -z "$database" ] || [ "$database" = "$after" ]; then
    echo "SUPERMETRY_DATABASE_URL must look like postgres://user:password@postgres:5432/database" >&2
    exit 1
  fi
  decode() {
    printf '%b' "$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/%\([0-9A-Fa-f][0-9A-Fa-f]\)/\\x\1/g')"
  }
  POSTGRES_USER="$(decode "$user")"
  POSTGRES_PASSWORD="$(decode "$password")"
  POSTGRES_DB="$(decode "$database")"
  export POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB
fi

exec docker-entrypoint.sh postgres
