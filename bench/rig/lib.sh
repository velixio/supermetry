# Shared helpers for bench/rig scripts. Source it; don't execute it.
# shellcheck shell=bash

RIG="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESULTS="$(cd "$RIG/.." && pwd)/results"
# shellcheck source=rig.conf
. "$RIG/rig.conf"
# shellcheck disable=SC1091
[ -f "$RIG/rig.local.conf" ] && . "$RIG/rig.local.conf"
# Not exported (no set -a): GoatCounter treats any GOATCOUNTER_* env var as a flag and
# refuses unknown ones, and secrets shouldn't leak into every child's environment anyway.
# shellcheck disable=SC1091
[ -f "$RIG/.env" ] && . "$RIG/.env"

mkdir -p "$RIG/run" "$RIG/logs" "$RIG/bin"

log() { printf '%s [%s] %s\n' "$(date -u +%H:%M:%S)" "${RIG_PRODUCT:-rig}" "$*" >&2; }
die() { log "ERROR: $*"; exit 1; }

# The user isn't in the docker group (that needs a re-login), so fall back to sudo.
if docker info >/dev/null 2>&1; then D=(docker); else D=(sudo docker); fi

# A realistic desktop UA: all three products drop known bots.
BENCH_UA='Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36'

products() { # expand plausible|umami|goatcounter|all
  case "${1:-}" in
    all) echo plausible umami goatcounter ;;
    plausible|umami|goatcounter) echo "$1" ;;
    *) echo "usage: $(basename "$0") plausible|umami|goatcounter|all" >&2; exit 2 ;;
  esac
}

now_ms() { date +%s%3N; }

require_env() {
  [ -f "$RIG/.env" ] || die "missing $RIG/.env; run $RIG/init-env.sh"
  local v; for v in "$@"; do [ -n "${!v:-}" ] || die "$v not set in .env"; done
}

# ---- local PG (peer auth as postgres; never restarts the server) -------------
pg_admin() { sudo -u postgres psql -X -v ON_ERROR_STOP=1 -Atq "$@"; }

ensure_pg_db() { # name password: role+db of the same name, idempotent
  local name=$1 pass=$2
  pg_admin -v n="$name" -v p="$pass" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'n', :'p')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'n') \gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'n', :'p') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'n', :'n')
 WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'n') \gexec
SQL
}

drop_pg_db() { # name: drop db and role (used by down.sh --purge)
  local name=$1
  pg_admin -c "DROP DATABASE IF EXISTS \"$name\" WITH (FORCE)"
  pg_admin -c "DROP ROLE IF EXISTS \"$name\""
}

# psql as the product role over TCP (password from .env via PGPASSWORD).
pg_as() { # role db [psql args...]
  local role=$1 db=$2; shift 2
  local pv; pv="$(echo "${role}_DB_PASSWORD" | tr '[:lower:]' '[:upper:]')"
  PGPASSWORD="${!pv}" psql -X -v ON_ERROR_STOP=1 -Atq -h "$PGHOST_LOCAL" -p "$PGPORT_LOCAL" -U "$role" -d "$db" "$@"
}

# ---- ports / http -----------------------------------------------------------
port_busy() { ss -Hltn "sport = :$1" | grep -q .; }
port_owner() { sudo ss -Hltnp "sport = :$1" | sed -n 's/.*users:((\("[^"]*"\),pid=\([0-9]*\).*/\1 pid \2/p' | head -1; }

wait_http() { # url timeout_s [expected-substring]
  local url=$1 t=$2 want=${3:-} i=0 body
  while [ "$i" -lt "$((t * 5))" ]; do
    if body=$(curl -fsS --max-time 2 "$url" 2>/dev/null); then
      [ -z "$want" ] || [[ "$body" == *"$want"* ]] && return 0
    fi
    sleep 0.2; i=$((i + 1))
  done
  return 1
}

# ---- plausible ----------------------------------------------------------------
PCE="$RIG/plausible/ce"
pcompose() { PLAUSIBLE_PORT="$PLAUSIBLE_PORT" "${D[@]}" compose -p plausible --project-directory "$PCE" "$@"; }
p_rpc() { pcompose exec -T plausible /app/bin/plausible rpc "$1"; } # mix release RPC into the running node
p_psql() { pcompose exec -T plausible_db psql -X -v ON_ERROR_STOP=1 -Atq -U postgres -d plausible_db "$@"; }
p_ch() { pcompose exec -T plausible_events_db clickhouse-client --database plausible_events_db -q "$1"; }

# ---- goatcounter ------------------------------------------------------------
GC_BIN="$RIG/bin/goatcounter"
GC_PID="$RIG/run/goatcounter.pid"
gc_db() { echo "postgresql+host=$PGHOST_LOCAL port=$PGPORT_LOCAL user=goatcounter dbname=goatcounter sslmode=disable"; }
gc() { PGPASSWORD="$GOATCOUNTER_DB_PASSWORD" "$GC_BIN" "$@"; }
gc_running() { [ -f "$GC_PID" ] && kill -0 "$(cat "$GC_PID")" 2>/dev/null; }

# ---- umami --------------------------------------------------------------------
UMAMI_CT=bench-umami
umami_url() { echo "http://127.0.0.1:$UMAMI_PORT"; }
umami_login() { # user password -> bearer token (empty on failure)
  U="$1" P="$2" python3 -I -c 'import json,os;print(json.dumps({"username":os.environ["U"],"password":os.environ["P"]}))' |
    curl -sS -X POST "$(umami_url)/api/auth/login" -H 'Content-Type: application/json' --data-binary @- |
    python3 -I -c 'import sys,json
try: print(json.load(sys.stdin).get("token",""))
except Exception: print("")'
}
umami_token() { umami_login "$UMAMI_ADMIN_USER" "$UMAMI_ADMIN_PASSWORD"; }

# jq is not installed (and installing it would be a system change), so JSON goes via python3.
json_get() { python3 -I -c 'import sys,json
d=json.load(sys.stdin)
for k in sys.argv[1].split("."):
    d=d[int(k)] if isinstance(d,list) else d[k]
print(d if not isinstance(d,(dict,list)) else json.dumps(d))' "$1"; }

sites_json_from_rows() { # stdin: "n<TAB>host<TAB>id[<TAB>extra]" rows -> JSON array on stdout
  python3 -I -c 'import sys,json
rows=[]
for line in sys.stdin.read().splitlines():
    if not line.strip(): continue
    f=line.split("\t")
    r={"site":int(f[0]),"host":f[1],"id":int(f[2]) if f[2].isdigit() else f[2]}
    if len(f)>3 and f[3]: r["user_id"]=int(f[3]) if f[3].isdigit() else f[3]
    rows.append(r)
rows.sort(key=lambda r:r["site"])
json.dump(rows,sys.stdout,indent=1); print()'
}
