# Shared by run.sh and tools/bench/envelope/run.sh.
# sm_init sets work, home, db_url, dbname, state; sm_start boots a server on a scratch
# database, completes first-run setup and signs in; sm_stop undoes it.
sm_init() {
  work="$(mktemp -d "${TMPDIR:-/tmp}/sm-live.XXXXXX")"
  home="$work/home"; mkdir -p "$home"
  admin_url="${SDK_COMPAT_ADMIN_DB:-postgres://supermetry:supermetry@127.0.0.1:5433/postgres?sslmode=disable}"
  dbname="sm_live_$$"
  db_url="${admin_url%/postgres?*}/$dbname?${admin_url#*\?}"
  state="$work/state.json"
  srv_pid=""
}
sm_stop() {
  [ -n "$srv_pid" ] && kill "$srv_pid" 2>/dev/null && wait "$srv_pid" 2>/dev/null
  psql "$admin_url" -qAtc "drop database if exists $dbname with (force)" >/dev/null 2>&1
}
# sm_start <binary> [extra env NAME=value...]
sm_start() {
  local bin="$1"; shift
  psql "$admin_url" -qAtc "create database $dbname" || { echo "cannot create scratch database on the dev Postgres"; return 1; }
  port=$(python3 -I -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])')
  base="http://127.0.0.1:$port"
  env -i HOME="$home" PATH=/usr/bin:/bin SUPERMETRY_DATABASE_URL="$db_url" \
    SUPERMETRY_SECRET_KEY="$(head -c32 /dev/urandom | od -An -tx1 | tr -d ' \n')" \
    SUPERMETRY_LISTEN="127.0.0.1:$port" SUPERMETRY_BASE_URL="$base" "$@" \
    "$bin" serve >"$work/server.out" 2>"$work/server.log" &
  srv_pid=$!
  for _ in $(seq 1 60); do
    curl -fsS "$base/api/private/setup/status" >/dev/null 2>&1 && break
    kill -0 "$srv_pid" 2>/dev/null || { echo "server exited:"; tail -20 "$work/server.log"; return 1; }
    sleep 0.5
  done
  python3 -I "$(dirname "${BASH_SOURCE[0]}")/check.py" bootstrap "$base" "$work/server.out" "$state" || { echo "bootstrap failed"; tail -5 "$work/server.log"; return 1; }
}
