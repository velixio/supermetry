#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
FINAL="$ROOT/bench/final"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

[[ -x "$FINAL/run.sh" ]] || fail "run.sh is not executable"

phases=(env panel-m panel-s panel-ingest panel-c2p1 panel-small plausible summary)
for phase in "${phases[@]}"; do
  out="$TMP/$phase"
  mkdir -p "$out"
  FINAL_RESULTS_DIR="$out" "$FINAL/run.sh" "$phase" --dry-run >"$TMP/$phase.stdout" 2>"$TMP/$phase.stderr"
  [[ -s "$out/$phase.log" ]] || fail "$phase did not write a log"
  [[ -s "$out/$phase.json" ]] || fail "$phase did not write JSON"
  python3 -I - "$out/$phase.json" "$phase" <<'PY'
import json, sys
p, phase = sys.argv[1:]
with open(p, encoding="utf-8") as f:
    x = json.load(f)
assert x["phase"] == phase, x
assert x["status"] in {"dry-run", "blocked"}, x
assert isinstance(x.get("commands", []), list), x
PY
done

# A second real invocation must resume without executing the phase body.
resume="$TMP/resume"
mkdir -p "$resume/.state"
printf '{"phase":"env","status":"complete"}\n' >"$resume/env.json"
: >"$resume/env.log"
: >"$resume/.state/env.done"
FINAL_RESULTS_DIR="$resume" "$FINAL/run.sh" env >"$TMP/resume.stdout"
grep -q 'already complete' "$TMP/resume.stdout" || fail "completion marker was ignored"

# Refuse malformed CLI and unknown phases.
if "$FINAL/run.sh" nope --dry-run >"$TMP/nope.out" 2>&1; then fail "unknown phase accepted"; fi
if "$FINAL/run.sh" env --bogus >"$TMP/bogus.out" 2>&1; then fail "unknown option accepted"; fi

# Shell syntax and mandatory strict mode in every shell entrypoint.
while IFS= read -r f; do
  bash -n "$f"
  grep -Eq '^set -euo pipefail$' "$f" || fail "$f lacks strict mode"
done < <(find "$FINAL" -type f -name '*.sh' | sort)

# Serializer snippets must accept exactly the arguments their shell passes.
PYTHONPYCACHEPREFIX="$TMP/pycache" python3 -m py_compile "$FINAL/http-replay.py" "$FINAL/freshness.py" "$FINAL/summary.py"
python3 -I "$FINAL/summary.py" "$TMP" "$TMP/summary.md" "$TMP/summary-data.json"
[[ $(grep -c '^| [A-Z][0-9]' "$TMP/summary.md") == 26 ]] || fail "summary does not contain every scorecard row"

printf 'PASS: final benchmark script contract (%d phases)\n' "${#phases[@]}"
