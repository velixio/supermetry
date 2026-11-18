#!/usr/bin/env bash
# End-to-end check of the Python auto-instrumentation wrapper, without a running
# Supermetry:
#   1. builds a tiny FastAPI fixture image (python:3.12-slim) and the wrapper
#      Dockerfile on top of it;
#   2. starts a throwaway OTLP sink (the pinned otelcol-contrib, OTLP/HTTP
#      receiver, file exporter) on the host;
#   3. runs the wrapped image (host network), calls /boom, stops the container;
#   4. asserts with jq that the sink received a span with an exception event
#      (ValueError, "bad id 7") and an ERROR log record for the same failure.
#
# The manual step, not done here: point OTEL_EXPORTER_OTLP_ENDPOINT/HEADERS at a
# real Supermetry App project and confirm the issue "ValueError: bad id 7".
#
# Needs docker. Cleans up its containers, images and temp files on exit.
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$here/../lib.sh"

echo "free RAM / disk before the build:"
free -h | sed -n 2p
df -h / | tail -1

tag=$$
fixture=supermetry-zc-fixture:$tag
wrapped=supermetry-zc-wrapped:$tag
cname=supermetry-zc-app-$tag
work=$(mktemp -d)
sink_pid=""
cleanup() {
  [ -n "$sink_pid" ] && kill "$sink_pid" 2>/dev/null || true
  docker rm -f "$cname" >/dev/null 2>&1 || true
  docker rmi -f "$wrapped" "$fixture" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

docker build -q -t "$fixture" "$here/testdata/fixture" >/dev/null
docker build -q -t "$wrapped" --build-arg BASE_IMAGE="$fixture" "$here" >/dev/null
echo "ok: images built ($(docker image inspect -f '{{.Size}}' "$wrapped" | awk '{printf "%.0f MB", $1/1e6}'))"

sink_port=$((20000 + RANDOM % 20000))
app_port=$((20000 + RANDOM % 20000))
SINK_PORT=$sink_port SINK_DIR=$work "$bin" --config "$here/testdata/sink.yaml" >"$work/sink.log" 2>&1 &
sink_pid=$!
for _ in $(seq 50); do
  curl -s -o /dev/null "http://127.0.0.1:$sink_port/" && break
  sleep 0.2
done

docker run -d --name "$cname" --network host \
  -e PORT="$app_port" \
  -e OTEL_SERVICE_NAME=fixture \
  -e OTEL_EXPORTER_OTLP_ENDPOINT="http://127.0.0.1:$sink_port" \
  -e OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf \
  -e OTEL_EXPORTER_OTLP_HEADERS="authorization=Bearer placeholder" \
  -e OTEL_TRACES_EXPORTER=otlp -e OTEL_LOGS_EXPORTER=otlp -e OTEL_METRICS_EXPORTER=none \
  -e OTEL_PYTHON_LOGGING_AUTO_INSTRUMENTATION_ENABLED=true \
  -e OTEL_BSP_SCHEDULE_DELAY=300 -e OTEL_BLRP_SCHEDULE_DELAY=300 \
  "$wrapped" >/dev/null

for _ in $(seq 60); do
  curl -s -o /dev/null "http://127.0.0.1:$app_port/health" && break
  sleep 0.5
done
curl -s -o /dev/null -w "GET /boom -> %{http_code}\n" "http://127.0.0.1:$app_port/boom"
curl -s -o /dev/null -w "GET /handled -> %{http_code}\n" "http://127.0.0.1:$app_port/handled"
sleep 3
docker stop -t 10 "$cname" >/dev/null
sleep 1
kill -INT "$sink_pid"; wait "$sink_pid" 2>/dev/null || true; sink_pid=""

fail() { echo "FAIL: $*"; docker logs "$cname" 2>&1 | tail -20; ls -la "$work"; exit 1; }
[ -s "$work/traces.jsonl" ] || fail "no traces reached the sink"
[ -s "$work/logs.jsonl" ] || fail "no logs reached the sink"

span_ok=$("$jq" -s '[.[].resourceSpans[].scopeSpans[].spans[].events[]?
  | select(.name=="exception")
  | [.attributes[] | {(.key): .value.stringValue}] | add
  | select(.["exception.type"]=="ValueError" and .["exception.message"]=="bad id 7")] | length' "$work/traces.jsonl")
[ "$span_ok" -ge 1 ] || fail "no span exception event ValueError: bad id 7"
echo "ok: span exception event ValueError: bad id 7 ($span_ok)"

# Uncaught errors inside uvicorn are logged to a logger that does not propagate
# to the root logger, so they reach Supermetry as the span exception above. An
# exception the app logs itself arrives as an ERROR log record:
log_ok=$("$jq" -s '[.[].resourceLogs[].scopeLogs[].logRecords[]
  | select(.severityNumber>=17 and .body.stringValue=="could not load item")
  | [.attributes[] | {(.key): .value.stringValue}] | add
  | select(.["exception.type"]=="ValueError" and .["exception.message"]=="bad id 7")] | length' "$work/logs.jsonl")
[ "$log_ok" -ge 1 ] || fail "no ERROR log record with exception.type ValueError"
echo "ok: ERROR log record with exception.type ValueError, message bad id 7 ($log_ok)"
echo PASS
