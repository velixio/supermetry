#!/usr/bin/env bash
# Usage: scrubcheck.sh <dir>...
# Fails (exit 1) when a fixture contains machine data: /home/, /Users/ or
# /root/ paths, this machine's hostname or username, an IPv4 outside
# 127.0.0.1 and 192.0.2.0/24, or an email address not at example.com.
# Envelope bodies are decompressed (gzip, zlib, br when the brotli module is
# importable) before scanning. Hostname and username match as whole tokens
# when short, because short names are ordinary words ("work", "user").
set -euo pipefail
[ $# -ge 1 ] || { echo "usage: $0 <dir>..." >&2; exit 2; }

HOST="$(hostname)"
ME="${USER:-$(id -un)}"
fail=0

PY=python3
[ -x "$(dirname "$0")/python/.venv/bin/python" ] && PY="$(cd "$(dirname "$0")" && pwd)/python/.venv/bin/python"

plain() {
  "$PY" -I - "$1" <<'PY'
import sys, gzip, zlib
d = open(sys.argv[1], 'rb').read()
if d[:2] == b'\x1f\x8b':
    d = gzip.decompress(d)
elif len(d) > 1 and d[0] == 0x78 and (d[0] * 256 + d[1]) % 31 == 0:
    try:
        d = zlib.decompress(d)
    except Exception:
        pass
else:
    try:
        import brotli
        try:
            d = brotli.decompress(d)
        except Exception:
            pass
    except ImportError:
        pass
    if d[:1] != b'{':
        sys.exit("scrubcheck: cannot decode " + sys.argv[1] + " (brotli needs python/.venv)")
sys.stdout.buffer.write(d)
PY
}

report() { echo "SCRUB FAIL [$1] $2: $3" >&2; fail=1; }

while IFS= read -r -d '' f; do
  case "$f" in
    *.envelope) txt="$(plain "$f" | tr -c '[:print:]\n\t' ' ')" ;;
    *) txt="$(tr -c '[:print:]\n\t' ' ' < "$f")" ;;
  esac

  if grep -qE '/home/|/Users/|/root/' <<<"$txt"; then
    report "$f" path "$(grep -oE '.{0,20}(/home/|/Users/|/root/).{0,30}' <<<"$txt" | head -1)"
  fi
  if [ -n "$HOST" ]; then
    if [ "${#HOST}" -ge 6 ]; then grep -qiF -- "$HOST" <<<"$txt" && report "$f" hostname "$HOST" || true
    else grep -qiwF -- "$HOST" <<<"$txt" && report "$f" hostname "$HOST" || true; fi
  fi
  if [ "${#ME}" -ge 6 ]; then
    grep -qiF -- "$ME" <<<"$txt" && report "$f" username "$ME" || true
  else # path-like or login-like uses only
    grep -qE "(/|\\\\|~)${ME}([/\\\\:]|\$)|(^|[^A-Za-z0-9_\"'.-])${ME}@" <<<"$txt" && report "$f" username "$ME" || true
  fi
  while read -r ip; do
    [ -z "$ip" ] && continue
    case "$ip" in 127.0.0.1|192.0.2.*) ;; *) report "$f" ipv4 "$ip" ;; esac
  done < <(grep -oE '(^|[^0-9.])[0-9]{1,3}(\.[0-9]{1,3}){3}($|[^0-9])' <<<"$txt" | grep -oE '[0-9]{1,3}(\.[0-9]{1,3}){3}' | sort -u || true)
  while read -r em; do
    [ -z "$em" ] && continue
    case "$em" in *@example.com) ;; *) report "$f" email "$em" ;; esac
  done < <(grep -oE '[[:alnum:]._+-]+@[[:alnum:].-]+\.[[:alpha:]]{2,}' <<<"$txt" | sort -u || true)
done < <(find "$@" -type f \( -name '*.envelope' -o -name '*.json' \) -print0)

[ "$fail" -eq 0 ] && echo "scrubcheck: ok"
exit "$fail"
