#!/usr/bin/env bash
# Install bench/rig/pg-tuning.conf as a conf.d drop-in for the local PG 18 and restart it.
#   (no flag)  dry run: show current -> profile values. Changes nothing (no /etc write, no reload).
#   --apply    copy it to conf.d/90-panel-bench.conf, restart postgresql@18-main, verify
#   --revert   remove the drop-in, restart, verify
# WARNING: a restart drops every connection on this server, including the other local apps' DBs
# (cryptoscan, pay, keeper, superadmin) and the running incumbents. Coordinate first, and run it once,
# before the measurement runs (bench/CONTRACT.md).
set -euo pipefail
RIG="$(cd "$(dirname "$0")" && pwd)"
CLUSTER=${PG_CLUSTER:-18/main}
CONF_D="/etc/postgresql/$CLUSTER/conf.d"
DROPIN="$CONF_D/90-panel-bench.conf"
UNIT="postgresql@${CLUSTER/\//-}"
PROFILE="$RIG/pg-tuning.conf"
mode=${1:-dry-run}
pg() { sudo -u postgres psql -X -Atq -v ON_ERROR_STOP=1 "$@"; }

names=$(sed -n 's/^\s*\([a-z_]*\)\s*=.*/\1/p' "$PROFILE" | paste -sd, -)
show() {
  pg -F ' | ' -c "SELECT name, current_setting(name) AS current, coalesce(f.setting,'-') AS in_files, pending_restart
    FROM pg_settings s LEFT JOIN LATERAL (SELECT setting FROM pg_file_settings f WHERE f.name = s.name AND f.applied IS NOT NULL ORDER BY seqno DESC LIMIT 1) f ON true
    WHERE name = ANY(string_to_array('$names', ',')) ORDER BY name"
}
check_errors() { # pg_file_settings re-parses the config files on every query, without applying them
  local e; e=$(pg -c "SELECT sourcefile||':'||sourceline||' '||coalesce(name,'')||' '||error FROM pg_file_settings WHERE error IS NOT NULL AND error <> 'setting could not be applied'")
  [ -z "$e" ] || { echo "config errors:" >&2; echo "$e" >&2; return 1; }
}
restart_and_verify() {
  check_errors
  echo "restarting $UNIT ..." >&2
  sudo systemctl restart "$UNIT"
  for _ in $(seq 60); do pg -c 'select 1' >/dev/null 2>&1 && break; sleep 0.5; done
  show
}

grep -q "^include_dir = 'conf.d'" "/etc/postgresql/$CLUSTER/postgresql.conf" || { echo "include_dir 'conf.d' not active" >&2; exit 1; }

case $mode in
  dry-run|--dry-run)
    # Touches nothing under /etc and sends no reload. Checks names against pg_settings and
    # shows current -> profile. Value syntax is checked by pg_file_settings in --apply before the restart.
    echo "profile: $PROFILE -> $DROPIN ($( [ -f "$DROPIN" ] && echo installed || echo 'not installed' ))"
    sed -n "s/^\s*\([a-z_]*\)\s*=\s*\([^#]*\).*/\1\t\2/p" "$PROFILE" | while IFS=$'\t' read -r n v; do
      cur=$(pg -c "SELECT current_setting('$n')" 2>/dev/null) || { echo "UNKNOWN parameter: $n" >&2; exit 1; }
      ctx=$(pg -c "SELECT context FROM pg_settings WHERE name = '$n'")
      printf '%-34s %-10s -> %-10s (%s)\n' "$n" "$cur" "$(echo "$v" | xargs)" "$ctx"
    done
    ;;
  --apply)
    sudo install -o postgres -g postgres -m 644 "$PROFILE" "$DROPIN"
    check_errors || { sudo rm -f "$DROPIN"; echo "drop-in removed; nothing restarted" >&2; exit 1; }
    restart_and_verify ;;
  --revert)
    sudo rm -f "$DROPIN"
    restart_and_verify ;;
  *) echo "usage: $0 [--apply|--revert]" >&2; exit 2 ;;
esac
