#!/usr/bin/env bash
# Checks otelcol-journald.yaml with a pinned otelcol-contrib.
#
#  1. `validate` on the real config (journald receiver, no journal access needed).
#  2. A test run: the real config is merged with an overlay that swaps the
#     journald receiver for a filelog reader over testdata/vssaas-journal.jsonl
#     (journal export format) and the otlphttp exporter for a file exporter. The
#     receiver's operators block is copied verbatim from the real config, so the
#     parsing under test is the parsing that ships. Assertions use jq.
#
# Tools come from lib.sh (pinned, checksummed, cached outside the repo).
set -euo pipefail

here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
. "$here/lib.sh"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
export SUPERMETRY_OTLP_ENDPOINT=http://127.0.0.1:1/api/1/otlp SUPERMETRY_DSN_KEY=placeholder

# 1. validate (the storage dir only exists on a real host, so point it at a temp dir)
cat >"$work/validate.yaml" <<EOF
extensions:
  file_storage:
    directory: $work/storage
EOF
"$bin" validate --config "$here/otelcol-journald.yaml" --config "$work/validate.yaml"
echo "ok: validate"

# 2. fixture run
{
  cat <<EOF
extensions: {}
receivers:
  filelog:
    include: [$here/testdata/vssaas-journal.jsonl]
    start_at: beginning
    operators:
      - type: json_parser
        parse_from: body
        parse_to: body
EOF
  sed -n '/# BEGIN operators/,/# END operators/p' "$here/otelcol-journald.yaml" |
    grep -v -e '# BEGIN operators' -e '# END operators' -e '^    operators:$' |
    cat
  cat <<EOF
exporters:
  file:
    path: $work/out.jsonl
service:
  extensions: []
  pipelines:
    logs:
      receivers: [filelog]
      exporters: [file]
EOF
} >"$work/test.yaml"

# Overlay lists replace the real config's lists, maps merge: the real processors run.
# The journald receiver stays defined but unused.
timeout -s INT 12 "$bin" --config "$here/otelcol-journald.yaml" --config "$work/test.yaml" \
  >"$work/run.log" 2>&1 || true
if [ ! -s "$work/out.jsonl" ]; then echo "FAIL: no output"; tail -30 "$work/run.log"; exit 1; fi

# Flatten to one record per line: {sev, body, attrs{}, svc}
"$jq" -c '.resourceLogs[] | . as $r | .scopeLogs[].logRecords[] | {
    sev: .severityNumber, body: .body.stringValue,
    attrs: ([.attributes[]? | {key, value: .value.stringValue}] | from_entries),
    svc: ([$r.resource.attributes[]? | select(.key=="service.name") | .value.stringValue][0]),
    env: ([$r.resource.attributes[]? | select(.key=="deployment.environment.name") | .value.stringValue][0])}' \
  "$work/out.jsonl" >"$work/records.jsonl"

fail() { echo "FAIL: $*"; cat "$work/records.jsonl"; exit 1; }
q() { "$jq" -s -r "$1" "$work/records.jsonl"; }

[ "$(q 'length')" = 4 ] || fail "expected 4 records (3 lines + 1 recombined panic)"
[ "$(q '[.[]|select(.sev==21)]|length')" = 1 ] || fail "expected one FATAL record"
[ "$(q '.[]|select(.sev==21)|.attrs["exception.type"]')" = panic ] || fail "panic exception.type"
[ "$(q '.[]|select(.sev==21)|.attrs["exception.message"]')" = "runtime error: invalid memory address or nil pointer dereference" ] || fail "panic message"
[ "$(q '.[]|select(.sev==21)|.attrs["exception.stacktrace"]|split("\n")|length')" = 12 ] || fail "panic stack should be 12 lines"
[ "$(q '.[]|select(.sev==21)|.svc')" = vssaas-sync ] || fail "service.name from unit"
[ "$(q '.[]|select(.body=="order 1001 failed")|.sev')" = 17 ] || fail "logfmt ERROR severity or body=msg"
[ "$(q '.[]|select(.body=="job 77 failed")|.sev')" = 17 ] || fail "JSON ERROR severity or body=msg"
[ "$(q '.[]|select(.body=="listening on :8080")|.sev')" = 9 ] || fail "INFO severity"
[ "$(q '[.[]|select(.env=="production")]|length')" = 4 ] || fail "deployment.environment.name"
echo "ok: fixture (4 records, panic recombined, severities, service.name)"
echo PASS
