#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

mode=${1:-quick}
shift || true
case "$mode" in
  quick|full) ;;
  compare) ;;
  *) echo "usage: scripts/api-bench.sh quick|full [flags] | compare BASELINE.json [--tolerance FRACTION]" >&2; exit 2 ;;
esac

run_root=.codex-runs/api-bench
bin_dir=$run_root/bin
mkdir -p "$bin_dir"
go build -tags benchtrace -o "$bin_dir/api-bench" ./tools/bench/api
go build -o "$bin_dir/gen" ./tools/gen
go build -o "$bin_dir/supermetry" ./cmd/supermetry

export SUPERMETRY_BENCH_DSN=${SUPERMETRY_BENCH_DSN:-postgres://supermetry:supermetry@127.0.0.1:5433/supermetry_bench?sslmode=disable}
stamp=$(date -u +%Y%m%d-%H%M%S-%N)

if [[ "$mode" == compare ]]; then
  baseline=${1:-}
  if [[ -z "$baseline" ]]; then
    echo "compare requires BASELINE.json" >&2
    exit 2
  fi
  shift
  output=$run_root/compare-$stamp
  "$bin_dir/api-bench" --mode quick --dsn "$SUPERMETRY_BENCH_DSN" --output "$output" \
    --generator-bin "$bin_dir/gen" --server-bin "$bin_dir/supermetry"
  "$bin_dir/api-bench" compare --baseline "$baseline" --current "$output/result.json" "$@"
  exit
fi

output=$run_root/$mode-$stamp
"$bin_dir/api-bench" --mode "$mode" --dsn "$SUPERMETRY_BENCH_DSN" --output "$output" \
  --generator-bin "$bin_dir/gen" --server-bin "$bin_dir/supermetry" "$@"
printf '%s\n' "$output"
