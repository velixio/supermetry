#!/usr/bin/env bash
# Create the admin user and sites site1.test … site$SITE_COUNT.test (UTC) for
# plausible|umami|goatcounter|all, then write tools/bench/rig/<product>-sites.json:
#   [{"site": N, "host": "siteN.test", "id": <product site id>[, "user_id": ...]}]
# Idempotent: existing users/sites are kept. Usage: seed.sh <product> [count]
set -euo pipefail
. "$(dirname "$0")/lib.sh"
target=${1:-}; N=${2:-$SITE_COUNT}

seed_plausible() {
  RIG_PRODUCT=plausible
  require_env PLAUSIBLE_ADMIN_EMAIL PLAUSIBLE_ADMIN_PASSWORD PLAUSIBLE_ADMIN_NAME
  log "admin + $N sites via release RPC (Plausible.Auth.User.new + Plausible.Sites.create)"
  # The Elixir is fed on stdin so the password isn't in the host-side `docker exec` argv.
  cat <<EOF | pcompose exec -T plausible sh -c '/app/bin/plausible rpc "$(cat)"'
alias Plausible.{Repo, Auth.User, Site, Sites}
email = "$PLAUSIBLE_ADMIN_EMAIL"
pw = "$PLAUSIBLE_ADMIN_PASSWORD"
user =
  case Repo.get_by(User, email: email) do
    nil -> User.new(%{name: "$PLAUSIBLE_ADMIN_NAME", email: email, password: pw, password_confirmation: pw}) |> Repo.insert!()
    u -> u
  end
created =
  for i <- 1..$N, reduce: 0 do
    acc ->
      d = "site#{i}.test"
      case Repo.get_by(Site, domain: d) do
        nil ->
          {:ok, _} = Sites.create(user, %{"domain" => d, "timezone" => "UTC"})
          acc + 1
        _ -> acc
      end
  end
IO.puts("created #{created} sites")
EOF
  p_psql -F $'\t' -c "SELECT substring(domain from '^site([0-9]+)\.test\$'), domain, id FROM sites WHERE domain ~ '^site[0-9]+\.test\$'" \
    | sites_json_from_rows >"$RIG/plausible-sites.json"
  log "$(grep -c '"site"' "$RIG/plausible-sites.json") sites -> plausible-sites.json"
}

seed_umami() {
  RIG_PRODUCT=umami
  require_env UMAMI_ADMIN_USER UMAMI_ADMIN_PASSWORD UMAMI_DB_PASSWORD
  local tok; tok=$(umami_token)
  if [ -z "$tok" ]; then
    # Fresh install: the migration seeds admin/umami (01_init/migration.sql). Rotate it.
    tok=$(umami_login admin umami)
    [ -n "$tok" ] || die "cannot log in with the configured or the default admin password"
    log "rotating default admin password"
    P="$UMAMI_ADMIN_PASSWORD" python3 -I -c 'import json,os;print(json.dumps({"currentPassword":"umami","newPassword":os.environ["P"]}))' |
      curl -fsS -o /dev/null -X POST "$(umami_url)/api/me/password" -H "Authorization: Bearer $tok" \
        -H 'Content-Type: application/json' --data-binary @-
    tok=$(umami_token); [ -n "$tok" ] || die "login with the new admin password failed"
  fi
  local have; have=$(pg_as umami umami -c "SELECT domain FROM website WHERE deleted_at IS NULL")
  local i c=0
  for i in $(seq 1 "$N"); do
    grep -qx "site$i.test" <<<"$have" && continue
    curl -fsS -o /dev/null -X POST "$(umami_url)/api/websites" -H "Authorization: Bearer $tok" \
      -H 'Content-Type: application/json' -d "{\"name\":\"site$i.test\",\"domain\":\"site$i.test\"}"
    c=$((c + 1))
  done
  log "created $c websites via POST /api/websites"
  pg_as umami umami -F $'\t' -c "SELECT substring(domain from '^site([0-9]+)\.test\$'), domain, website_id FROM website WHERE deleted_at IS NULL AND domain ~ '^site[0-9]+\.test\$'" \
    | sites_json_from_rows >"$RIG/umami-sites.json"
  log "$(grep -c '"site"' "$RIG/umami-sites.json") sites -> umami-sites.json"
}

seed_goatcounter() {
  RIG_PRODUCT=goatcounter
  require_env GOATCOUNTER_ADMIN_EMAIL GOATCOUNTER_ADMIN_PASSWORD GOATCOUNTER_DB_PASSWORD
  # One independent site per vhost, each with its own user: GoatCounter's timezone is a
  # *user* setting (settings.go UserSettings.Timezone), so this is what allows per-site zones.
  local have; have=$(pg_as goatcounter goatcounter -c "SELECT cname FROM sites")
  local i c=0
  for i in $(seq 1 "$N"); do
    grep -qx "site$i.test" <<<"$have" && continue
    gc db create site -db "$(gc_db)" -vhost "site$i.test" \
      -user.email "$GOATCOUNTER_ADMIN_EMAIL" -user.password "$GOATCOUNTER_ADMIN_PASSWORD" >>"$RIG/logs/goatcounter.log" 2>&1 \
      || die "db create site site$i.test failed; see logs/goatcounter.log"
    c=$((c + 1))
  done
  log "created $c sites via 'goatcounter db create site'"
  pg_as goatcounter goatcounter -F $'\t' -c "SELECT substring(s.cname from '^site([0-9]+)\.test\$'), s.cname, s.site_id, min(u.user_id) FROM sites s LEFT JOIN users u ON u.site_id = s.site_id WHERE s.cname ~ '^site[0-9]+\.test\$' GROUP BY 1,2,3" \
    | sites_json_from_rows >"$RIG/goatcounter-sites.json"
  log "$(grep -c '"site"' "$RIG/goatcounter-sites.json") sites -> goatcounter-sites.json"
  # GoatCounter caches sites by host; a restart isn't needed (cache misses fall through to the DB).
}

for p in $(products "$target"); do "seed_$p"; done
