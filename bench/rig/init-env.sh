#!/usr/bin/env bash
# Create bench/rig/.env (chmod 600) with random secrets, if it doesn't exist yet.
# Idempotent: an existing .env is never overwritten. Prints no secrets.
set -euo pipefail
RIG="$(cd "$(dirname "$0")" && pwd)"
ENV="$RIG/.env"
if [ -f "$ENV" ]; then echo "exists: $ENV (left unchanged)"; chmod 600 "$ENV"; exit 0; fi

rnd() { tr -dc 'A-Za-z0-9' </dev/urandom | head -c "${1:-24}"; }
umask 077
cat >"$ENV" <<EOF
# bench/rig secrets. Generated $(date -u +%FT%TZ) by init-env.sh. chmod 600; never commit or print.

# Plausible CE (written into plausible/ce/.env by up.sh; that file is also 600)
PLAUSIBLE_SECRET_KEY_BASE=$(rnd 86)
PLAUSIBLE_ADMIN_NAME=BenchAdmin
PLAUSIBLE_ADMIN_EMAIL=admin@bench.test
PLAUSIBLE_ADMIN_PASSWORD=$(rnd 24)

# Umami (local PG role/db "umami"; written into run/umami.env by up.sh)
UMAMI_DB_PASSWORD=$(rnd 24)
UMAMI_APP_SECRET=$(rnd 48)
UMAMI_TWO_FACTOR_ENCRYPTION_KEY=$(openssl rand -hex 32)
UMAMI_ADMIN_USER=admin
UMAMI_ADMIN_PASSWORD=$(rnd 24)

# GoatCounter (local PG role/db "goatcounter"; one user per site, same email+password)
GOATCOUNTER_DB_PASSWORD=$(rnd 24)
GOATCOUNTER_ADMIN_EMAIL=admin@bench.test
GOATCOUNTER_ADMIN_PASSWORD=$(rnd 24)
EOF
chmod 600 "$ENV"
echo "created: $ENV (600)"
