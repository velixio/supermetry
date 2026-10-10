#!/bin/sh
# Starts the bundled PostgreSQL. When POSTGRES_PASSWORD is not set, take the role,
# password and database name from SUPERMETRY_DATABASE_URL
# (postgres://user:password@postgres:5432/dbname?...), so there is one place to
# configure them. Anything the URL leaves out defaults to "supermetry", including
# the password. The password may be percent-encoded, as in the URL.
set -eu

decode() {
  printf '%b' "$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/%\([0-9A-Fa-f][0-9A-Fa-f]\)/\\x\1/g')"
}

if [ -z "${POSTGRES_PASSWORD:-}" ]; then
  user=""
  password=""
  database=""
  if [ -n "${SUPERMETRY_DATABASE_URL:-}" ]; then
    rest="${SUPERMETRY_DATABASE_URL#*://}"
    case "$rest" in
      *@*)
        credentials="${rest%%@*}"
        after="${rest#*@}"
        user="${credentials%%:*}"
        case "$credentials" in
          *:*) password="${credentials#*:}" ;;
        esac
        ;;
      *) after="$rest" ;;
    esac
    case "$after" in
      */*)
        database="${after#*/}"
        database="${database%%\?*}"
        ;;
    esac
  fi
  POSTGRES_USER="$(decode "${user:-supermetry}")"
  POSTGRES_PASSWORD="$(decode "${password:-supermetry}")"
  POSTGRES_DB="$(decode "${database:-supermetry}")"
  export POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB
fi

exec docker-entrypoint.sh postgres
