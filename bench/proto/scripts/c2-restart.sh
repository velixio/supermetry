#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
schema="panel_c2_${$}"
port=${PANEL_C2_PORT:-18092}
db_dsn=${PANEL_TEST_DSN:-postgres://panel:panel@127.0.0.1:5432/panel?sslmode=disable}
pid=
export PGPASSWORD=${PGPASSWORD:-panel}
export PGOPTIONS="-c search_path=$schema"
cleanup() {
  if [[ -n ${pid} ]] && kill -0 "$pid" 2>/dev/null; then kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  PGOPTIONS= psql "$db_dsn" -v ON_ERROR_STOP=1 -c "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

PGOPTIONS= psql "$db_dsn" -v ON_ERROR_STOP=1 -c "CREATE SCHEMA $schema" >/dev/null
(cd "$root" && go build -o "$tmp/proto" .)
printf '[{"id":1,"host":"restart.test","timezone":"UTC","share":1}]\n' >"$tmp/sites.json"

start_server() {
	local session_flush=${1:-1h}
  PANEL_DSN="$db_dsn" "$tmp/proto" serve --addr="127.0.0.1:$port" --sites="$tmp/sites.json" --geoip='' \
    --batch=1 --flush=10ms --session-flush="$session_flush" >>"$tmp/server.log" 2>&1 &
  pid=$!
  for _ in {1..100}; do
    curl -fsS "http://127.0.0.1:$port/api/stats" >/dev/null 2>&1 && return
    sleep 0.05
  done
  return 1
}
post_page() {
  local path=$1
  curl -fsS -o /dev/null -A 'Mozilla/5.0 PanelRestartTest/1.0' -H 'X-Forwarded-For: 198.51.100.77' \
    -H 'Content-Type: text/plain' --data "{\"n\":\"pageview\",\"u\":\"https://restart.test$path\",\"d\":\"restart.test\"}" \
    "http://127.0.0.1:$port/api/event"
}
wait_sql() {
  local query=$1 want=$2
  for _ in {1..200}; do
    [[ $(psql "$db_dsn" -Atc "$query") == "$want" ]] && return
    sleep 0.05
  done
  printf 'timed out: %s != %s\n' "$query" "$want" >&2
  return 1
}

# Persist the first event cleanly, then make the durable event log one event
# newer than the sessions snapshot and crash before the next session flush.
start_server
post_page /one
wait_sql 'select count(*) from events' 1
wait_sql 'select count(*) from salts' 1
kill -TERM "$pid"
wait "$pid"
pid=
wait_sql 'select coalesce(sum(pageviews),0) from sessions' 1

start_server
post_page /two
wait_sql 'select count(*) from events' 2
wait_sql 'select coalesce(sum(pageviews),0) from sessions' 1
kill -9 "$pid"
wait "$pid" 2>/dev/null || true
pid=

start_server
wait_sql 'select coalesce(sum(pageviews),0) from sessions' 2
post_page /three
wait_sql 'select count(*) from events' 3
hard_sessions=$(psql "$db_dsn" -Atc 'select count(*) from sessions')
kill -TERM "$pid"
wait "$pid"
pid=

events=$(psql "$db_dsn" -Atc 'select count(*) from events')
sessions=$(psql "$db_dsn" -Atc 'select count(*) from sessions')
pageviews=$(psql "$db_dsn" -Atc 'select coalesce(sum(pageviews),0) from sessions')
rehydrated=$(grep -c 'rehydrated open sessions.*count=1' "$tmp/server.log" || true)
pass=false
[[ $hard_sessions == 1 && $events == 3 && $sessions == 1 && $pageviews == 3 && $rehydrated -ge 2 ]] && pass=true
printf '{"line":"C2","pass":%s,"hard_kill":{"events":2,"persisted_session_pageviews":1},"after_restart":{"events":%s,"sessions":%s,"pageviews":%s},"rehydrations":%s,"signals":["kill -9","SIGTERM"],"isolation":"schema:%s"}\n' \
  "$pass" "$events" "$sessions" "$pageviews" "$rehydrated" "$schema"
[[ $pass == true ]]
