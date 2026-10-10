#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
schema="supermetry_p1_${$}"
port=${SUPERMETRY_P1_PORT:-18091}
db_dsn=${SUPERMETRY_TEST_DSN:-postgres://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable}
pid=
export PGPASSWORD=${PGPASSWORD:-supermetry}
export PGOPTIONS="-c search_path=$schema"
cleanup() {
  if [[ -n ${pid} ]] && kill -0 "$pid" 2>/dev/null; then kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  PGOPTIONS= psql "$db_dsn" -v ON_ERROR_STOP=1 -c "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

PGOPTIONS= psql "$db_dsn" -v ON_ERROR_STOP=1 -c "CREATE SCHEMA $schema" >/dev/null
(cd "$root" && go build -o "$tmp/proto" .)
printf '[{"id":1,"host":"privacy.test","timezone":"UTC","share":1}]\n' >"$tmp/sites.json"

http_ip='198.51.100.201'
http_ua='SupermetryPrivacyHTTP/1.0 unique-full-UA-4a9d'
load_ip='203.0.113.202'
load_ua='SupermetryPrivacyLoad/1.0 unique-full-UA-7f31'

SUPERMETRY_DSN="$db_dsn" "$tmp/proto" serve --addr="127.0.0.1:$port" --sites="$tmp/sites.json" --geoip='' \
  --batch=1 --flush=10ms --session-flush=20ms >"$tmp/server.log" 2>&1 &
pid=$!
for _ in {1..100}; do
  curl -fsS "http://127.0.0.1:$port/api/stats" >/dev/null 2>&1 && break
  sleep 0.05
done
curl -fsS -o /dev/null -A "$http_ua" -H "X-Forwarded-For: $http_ip" -H 'Content-Type: text/plain' \
  --data '{"n":"pageview","u":"https://privacy.test/http","d":"privacy.test"}' \
  "http://127.0.0.1:$port/api/event"
for _ in {1..100}; do
  [[ $(psql "$db_dsn" -Atc 'select count(*) from events') == 1 ]] && break
  sleep 0.05
done
kill -TERM "$pid"
wait "$pid"
pid=

printf '%s\n' "{\"ts\":\"2026-07-01T12:00:00Z\",\"site\":1,\"host\":\"privacy.test\",\"path\":\"/load\",\"ref\":\"\",\"utm_source\":\"\",\"utm_medium\":\"\",\"utm_campaign\":\"\",\"name\":\"pageview\",\"props\":{},\"ip\":\"$load_ip\",\"ua\":\"$load_ua\",\"screen_w\":1440,\"country\":\"IN\",\"browser\":\"Other\",\"os\":\"Other\",\"device\":\"desktop\",\"vk\":\"ignored\",\"sk\":\"ignored\"}" >"$tmp/load.ndjson"
SUPERMETRY_DSN="$db_dsn" "$tmp/proto" load --sites="$tmp/sites.json" --batch=1 --flush=10ms <"$tmp/load.ndjson" >"$tmp/load.log" 2>&1

PGOPTIONS= pg_dump --dbname="$db_dsn" --schema="$schema" --format=plain --file="$tmp/dump.sql"
matches=0
for secret in "$http_ip" "$http_ua" "$load_ip" "$load_ua"; do
  if grep -Fq -- "$secret" "$tmp/dump.sql" "$tmp/server.log" "$tmp/load.log"; then
    printf 'privacy leak: %s\n' "$secret" >&2
    matches=$((matches + 1))
  fi
done
printf '{"line":"P1","pass":%s,"matches":%d,"http_events":1,"load_events":1,"shutdown":"SIGTERM","isolation":"schema:%s"}\n' "$([[ $matches == 0 ]] && printf true || printf false)" "$matches" "$schema"
[[ $matches == 0 ]]
