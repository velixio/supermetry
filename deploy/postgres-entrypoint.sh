#!/bin/sh
# Starts the bundled PostgreSQL. The role, password and database come from
# POSTGRES_USER, POSTGRES_PASSWORD and POSTGRES_DB (read from ../.env), and each
# defaults to "supermetry". SUPERMETRY_DATABASE_URL is not read here: the
# application builds its own connection URL from the same three settings.
set -eu

if [ -z "${POSTGRES_PASSWORD:-}" ]; then
  echo "WARNING: POSTGRES_PASSWORD is not set, so the bundled PostgreSQL uses the default password 'supermetry'." >&2
  echo "WARNING: that is only acceptable while this container publishes no port. Set POSTGRES_PASSWORD in the .env file next to the repository root." >&2
fi

POSTGRES_USER="${POSTGRES_USER:-supermetry}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-supermetry}"
POSTGRES_DB="${POSTGRES_DB:-supermetry}"
export POSTGRES_USER POSTGRES_PASSWORD POSTGRES_DB

exec docker-entrypoint.sh postgres
