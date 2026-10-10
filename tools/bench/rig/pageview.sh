#!/usr/bin/env bash
# Send ONE pageview for site1.test through each product's real tracking endpoint, then poll
# its storage until the event is visible. Prints one JSON object per product on stdout:
#   {"product","path","attempts","accepted_ms","visible_ms"}   (ms measured from the first send)
# Usage: pageview.sh plausible|umami|goatcounter|all [site-number]
set -euo pipefail
. "$(dirname "$0")/lib.sh"
target=${1:-}; S=${2:-1}
HOST="site$S.test"

site_id() { python3 -I -c 'import sys,json
for r in json.load(open(sys.argv[1])):
    if r["site"]==int(sys.argv[2]): print(r["id"])' "$RIG/$1-sites.json" "$S"; }

poll_visible() { # cmd... ; polls every 0.25s up to 120s until cmd prints a number >= 1
  local i n
  for i in $(seq 480); do
    n=$("$@" 2>/dev/null | tr -dc 0-9); [ "${n:-0}" -ge 1 ] && return 0; sleep 0.25
  done
  return 1
}

emit() { printf '{"product":"%s","host":"%s","path":"%s","attempts":%s,"accepted_ms":%s,"visible_ms":%s}\n' "$@"; }

pv_plausible() {
  RIG_PRODUCT=plausible
  local id path t0 t1 t2 a=0 hdr; id=$(site_id plausible); path="/rig-pv-$(date +%s%N)"
  t0=$(now_ms)
  # A new site is dropped (x-plausible-dropped) until the site cache's 30s "recently updated"
  # refresh picks it up, so retry until accepted. That wait is part of time-to-first-pageview.
  while :; do
    a=$((a + 1))
    hdr=$(curl -sS -o /dev/null -D - -X POST "http://127.0.0.1:$PLAUSIBLE_PORT/api/event" \
      -H "User-Agent: $BENCH_UA" -H 'X-Forwarded-For: 203.0.113.7' -H 'Content-Type: application/json' \
      -d "{\"name\":\"pageview\",\"url\":\"http://$HOST$path\",\"domain\":\"$HOST\",\"referrer\":\"\"}")
    if grep -q '^HTTP/[0-9.]* 202' <<<"$hdr" && ! grep -qi '^x-plausible-dropped' <<<"$hdr"; then break; fi
    [ "$(( $(now_ms) - t0 ))" -lt 120000 ] || die "event still dropped after 120s"
    sleep 1
  done
  t1=$(now_ms)
  poll_visible p_ch "SELECT count() FROM events_v2 WHERE site_id = $id AND pathname = '$path'" || die "not visible in events_v2"
  t2=$(now_ms)
  emit plausible "$HOST" "$path" "$a" "$((t1 - t0))" "$((t2 - t0))"
}

pv_umami() {
  RIG_PRODUCT=umami
  local id path t0 t1 t2 a=1 body; id=$(site_id umami); path="/rig-pv-$(date +%s%N)"
  t0=$(now_ms)
  body=$(curl -fsS -X POST "$(umami_url)/api/send" -H "User-Agent: $BENCH_UA" -H 'X-Forwarded-For: 203.0.113.7' \
    -H 'Content-Type: application/json' \
    -d "{\"type\":\"event\",\"payload\":{\"website\":\"$id\",\"hostname\":\"$HOST\",\"url\":\"$path\",\"title\":\"rig\",\"referrer\":\"\",\"screen\":\"1440x900\",\"language\":\"en-US\"}}")
  [[ "$body" == *beep* ]] && die "umami treated the request as a bot: $body"
  t1=$(now_ms)
  poll_visible pg_as umami umami -c "SELECT count(*) FROM website_event WHERE website_id = '$id' AND url_path = '$path'" || die "not visible in website_event"
  t2=$(now_ms)
  emit umami "$HOST" "$path" "$a" "$((t1 - t0))" "$((t2 - t0))"
}

pv_goatcounter() {
  RIG_PRODUCT=goatcounter
  local id path t0 t1 t2 a=1 hdr; id=$(site_id goatcounter); path="/rig-pv-$(date +%s%N)"
  t0=$(now_ms)
  # The same GET count.js sends; GoatCounter picks the site from the Host header (sites.cname).
  hdr=$(curl -sS -o /dev/null -D - "http://127.0.0.1:$GOATCOUNTER_PORT/count?p=$path&t=rig&r=&s=1440,900,1&rnd=$RANDOM" \
    -H "Host: $HOST" -H "User-Agent: $BENCH_UA")
  grep -q '^HTTP/[0-9.]* 200' <<<"$hdr" || die "count rejected: $(grep -i '^\(HTTP\|x-goatcounter\)' <<<"$hdr" | tr -d '\r' | tr '\n' ' ')"
  t1=$(now_ms)
  # Hits are buffered in memory and persisted every -store-every=10s. The default site setting
  # (collect=190) has no CollectHits bit, so the raw `hits` table stays empty: check the
  # hourly aggregate the dashboard reads instead.
  poll_visible pg_as goatcounter goatcounter -c "SELECT coalesce(sum(hc.total),0) FROM hit_counts hc JOIN paths p ON p.path_id = hc.path_id WHERE hc.site_id = $id AND p.path = '$path'" || die "not visible in hit_counts"
  t2=$(now_ms)
  emit goatcounter "$HOST" "$path" "$a" "$((t1 - t0))" "$((t2 - t0))"
}

for p in $(products "$target"); do "pv_$p"; done
