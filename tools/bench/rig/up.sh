#!/usr/bin/env bash
# Start (installing on first run) plausible|umami|goatcounter|all, and wait until healthy.
# Idempotent. First run = clone/pull/download + DB role/db creation + migrations.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
[ -f "$RIG/.env" ] || { "$RIG/init-env.sh" >&2; . "$RIG/.env"; }

check_port() { # port [allowed-owner]: fail if something else already listens there
  local p=$1 allow=${2:-} owner
  port_busy "$p" || return 0
  owner=$(port_owner "$p")
  [ -n "$allow" ] && [[ "$owner" == "\"$allow\""* ]] && return 0
  die "port $p is taken by ${owner:-unknown}; set a free port in rig.local.conf"
}

up_plausible() {
  RIG_PRODUCT=plausible
  require_env PLAUSIBLE_SECRET_KEY_BASE
  if [ ! -f "$PCE/compose.yml" ]; then
    log "cloning plausible/community-edition@$PLAUSIBLE_CE_REF"
    git clone -q --depth 1 --single-branch -b "$PLAUSIBLE_CE_REF" https://github.com/plausible/community-edition.git "$PCE"
  fi
  # Exactly the README quick start: .env with BASE_URL + SECRET_KEY_BASE + HTTP_PORT, and an override exposing the port.
  (umask 077; printf 'BASE_URL=http://localhost:%s\nSECRET_KEY_BASE=%s\nHTTP_PORT=80\n' \
     "$PLAUSIBLE_PORT" "$PLAUSIBLE_SECRET_KEY_BASE" >"$PCE/.env")
  cat >"$PCE/compose.override.yml" <<'EOF'
# tools/bench/rig: README step 3 for local evaluation (BASE_URL=http://localhost:8000, HTTP_PORT=80),
# bound to loopback only.
services:
  plausible:
    ports:
      - 127.0.0.1:${PLAUSIBLE_PORT:-8000}:80
EOF
  check_port "$PLAUSIBLE_PORT" docker-proxy
  log "compose up ($PLAUSIBLE_IMAGE)"
  pcompose up -d --quiet-pull >&2
  log "waiting for /api/system/health/ready"
  wait_http "http://127.0.0.1:$PLAUSIBLE_PORT/api/system/health/ready" 300 || die "plausible not ready after 300s"
  log "ready on :$PLAUSIBLE_PORT"
}

up_umami() {
  RIG_PRODUCT=umami
  require_env UMAMI_DB_PASSWORD UMAMI_APP_SECRET UMAMI_TWO_FACTOR_ENCRYPTION_KEY
  if "${D[@]}" ps --format '{{.Names}}' | grep -qx "$UMAMI_CT"; then
    wait_http "$(umami_url)/api/heartbeat" 120 && { log "already running on :$UMAMI_PORT"; return; }
  fi
  check_port "$UMAMI_PORT"
  ensure_pg_db umami "$UMAMI_DB_PASSWORD"
  (umask 077; printf 'DATABASE_URL=postgresql://umami:%s@localhost:%s/umami\nAPP_SECRET=%s\nTWO_FACTOR_ENCRYPTION_KEY=%s\nPORT=%s\nHOSTNAME=127.0.0.1\n' \
     "$UMAMI_DB_PASSWORD" "$PGPORT_LOCAL" "$UMAMI_APP_SECRET" "$UMAMI_TWO_FACTOR_ENCRYPTION_KEY" "$UMAMI_PORT" >"$RIG/run/umami.env")
  "${D[@]}" rm -f "$UMAMI_CT" >/dev/null 2>&1 || true
  log "docker run $UMAMI_IMAGE (--network host, :$UMAMI_PORT)"
  # Same knobs as Umami's own docker-compose.yml (init, restart, DATABASE_URL, APP_SECRET, 2FA key);
  # --network host so it reaches the local PG on localhost:5432.
  "${D[@]}" run -d --name "$UMAMI_CT" --network host --init --restart unless-stopped \
    --env-file "$RIG/run/umami.env" "$UMAMI_IMAGE" >/dev/null
  log "waiting for /api/heartbeat (runs prisma migrate deploy first)"
  wait_http "$(umami_url)/api/heartbeat" 300 || die "umami not up after 300s; see: ${D[*]} logs $UMAMI_CT"
  log "ready on :$UMAMI_PORT"
}

up_goatcounter() {
  RIG_PRODUCT=goatcounter
  require_env GOATCOUNTER_DB_PASSWORD
  if gc_running; then log "already running (pid $(cat "$GC_PID")) on :$GOATCOUNTER_PORT"; return; fi
  if [ ! -x "$RIG/bin/goatcounter-$GOATCOUNTER_VERSION" ]; then
    log "downloading $GOATCOUNTER_URL"
    curl -fsSL --retry 3 -o "$RIG/bin/goatcounter-$GOATCOUNTER_VERSION.gz" "$GOATCOUNTER_URL"
    gzip -df "$RIG/bin/goatcounter-$GOATCOUNTER_VERSION.gz"
    chmod +x "$RIG/bin/goatcounter-$GOATCOUNTER_VERSION"
  fi
  ln -sfn "goatcounter-$GOATCOUNTER_VERSION" "$GC_BIN"
  check_port "$GOATCOUNTER_PORT"
  ensure_pg_db goatcounter "$GOATCOUNTER_DB_PASSWORD"
  log "db migrate all -createdb (creates the schema in the empty db, then applies migrations)"
  gc db migrate all -createdb -db "$(gc_db)" >>"$RIG/logs/goatcounter.log" 2>&1 \
    || die "goatcounter db migrate failed; see logs/goatcounter.log"
  log "serve on 127.0.0.1:$GOATCOUNTER_PORT (plain HTTP)"
  # Defaults otherwise (-dbconn 16,4, -store-every 10, -ratelimit count:4/1 ...).
  PGPASSWORD="$GOATCOUNTER_DB_PASSWORD" setsid nohup "$GC_BIN" serve -db "$(gc_db)" \
    -listen "127.0.0.1:$GOATCOUNTER_PORT" -tls http -automigrate \
    >>"$RIG/logs/goatcounter.log" 2>&1 </dev/null &
  echo $! >"$GC_PID"
  wait_http "http://127.0.0.1:$GOATCOUNTER_PORT/status" 60 || die "goatcounter not up; see logs/goatcounter.log"
  log "ready on :$GOATCOUNTER_PORT (pid $(cat "$GC_PID"))"
}

for p in $(products "${1:-}"); do "up_$p"; done
