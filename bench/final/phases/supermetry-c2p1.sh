#!/usr/bin/env bash
set -euo pipefail
PHASE=supermetry-c2p1; source "$(dirname "$0")/../lib.sh"
say "ETA: 5-10 minutes"
ART="$RESULTS_DIR/artifacts/supermetry-c2p1"
BASE='postgresql://supermetry:supermetry@127.0.0.1:5432/supermetry?sslmode=disable'; schema="supermetry_final_c2p1_$$"; port=18094
plan "create disposable schema $schema; generate tiny tier (~33K events)"
plan "HTTP replay first half, wait durable, kill -9, restart, replay second half, graceful stop"
plan "compare accepted truth to stored events/session totals; grep schema pg_dump and logs for every generated IP/UA; drop schema"
(( DRY_RUN )) && dry_finish
fresh_artifacts "$ART"
require psql; require pg_dump; require curl
PROTO="$ROOT/bench/proto/proto"; GEN="$ROOT/bench/load/bin/gen"; [[ -x $PROTO && -x $GEN ]] || exit 1
dsn="$BASE&options=-csearch_path%3D$schema"; pid=
cleanup(){ [[ -z ${pid:-} ]] || { kill -TERM "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; }; psql "$BASE" -v ON_ERROR_STOP=1 -c "DROP SCHEMA IF EXISTS $schema CASCADE" >/dev/null 2>&1 || true; }
trap cleanup EXIT
psql "$BASE" -v ON_ERROR_STOP=1 -c "CREATE SCHEMA $schema" >/dev/null
"$GEN" --tier tiny --seed 42 --out "$ART/events.ndjson" --sites "$ART/sites.json" --truth "$ART/generator-truth.json" --quiet
python3 -I - "$ART/sites.json" <<'PY'
import json,sys
p=sys.argv[1]; x=json.load(open(p)); x.append({'id':101,'host':'restart.test','timezone':'UTC','share':0}); json.dump(x,open(p,'w'),indent=2)
PY
total=$(wc -l <"$ART/events.ndjson"); half=$((total/2))
start(){ SUPERMETRY_DSN="$dsn" "$PROTO" serve --addr="127.0.0.1:$port" --sites="$ART/sites.json" --geoip='' --batch=1 --flush=10ms --session-flush=20ms >>"$ART/server.log" 2>&1 & pid=$!; for _ in {1..200}; do curl -fsS "http://127.0.0.1:$port/api/stats" >/dev/null 2>&1 && return; sleep .05; done; return 1; }
wait_count(){ local want=$1; for _ in {1..600}; do [[ $(psql "$dsn" -Atc 'select count(*) from events') == "$want" ]] && [[ $(psql "$dsn" -Atc 'select coalesce(sum(pageviews+custom_events),0) from sessions') == "$want" ]] && return; sleep .1; done; return 1; }
start
"$FINAL_DIR/http-replay.py" --url "http://127.0.0.1:$port" --input "$ART/events.ndjson" --to-line "$half" --secrets "$ART/secrets.txt" --out "$ART/first.json"
probe_ip=198.51.100.77; probe_ua='Mozilla/5.0 SupermetryRestartTest/1.0'
curl -fsS -o /dev/null -A "$probe_ua" -H "X-Forwarded-For: $probe_ip" -H 'Content-Type: text/plain' --data '{"n":"pageview","u":"https://restart.test/final-restart-probe-a","d":"restart.test"}' "http://127.0.0.1:$port/api/event"
curl -fsS -o /dev/null -A "$probe_ua" -H "X-Forwarded-For: $probe_ip" -H 'Content-Type: text/plain' --data '{"n":"pageview","u":"https://restart.test/final-restart-probe-b","d":"restart.test"}' "http://127.0.0.1:$port/api/event"
wait_count "$((half+2))"
for _ in {1..200}; do [[ $(psql "$dsn" -Atc "select count(*) from sessions where entry_path='/final-restart-probe-a' and pageviews=2") == 1 ]] && break; sleep .05; done
[[ $(psql "$dsn" -Atc "select count(*) from sessions where entry_path='/final-restart-probe-a' and pageviews=2") == 1 ]]
for _ in {1..200}; do [[ $(psql "$dsn" -Atc 'select count(*) from salts where site_id=101') -ge 1 ]] && break; sleep .05; done
[[ $(psql "$dsn" -Atc 'select count(*) from salts where site_id=101') -ge 1 ]]
kill -9 "$pid"; wait "$pid" 2>/dev/null || true; pid=
start
"$FINAL_DIR/http-replay.py" --url "http://127.0.0.1:$port" --input "$ART/events.ndjson" --from-line "$((half+1))" --out "$ART/second.json"
curl -fsS -o /dev/null -A "$probe_ua" -H "X-Forwarded-For: $probe_ip" -H 'Content-Type: text/plain' --data '{"n":"pageview","u":"https://restart.test/final-restart-probe-c","d":"restart.test"}' "http://127.0.0.1:$port/api/event"
expected=$((total+3)); wait_count "$expected"
kill -TERM "$pid"; wait "$pid"; pid=
pg_dump "$dsn" --schema "$schema" --format plain --file "$ART/schema.sql"
privacy_matches=$(grep -F -f "$ART/secrets.txt" "$ART/schema.sql" "$ART/server.log" | wc -l || true)
events=$(psql "$dsn" -Atc 'select count(*) from events'); session_events=$(psql "$dsn" -Atc 'select coalesce(sum(pageviews+custom_events),0) from sessions')
duplicate_sessions=$(psql "$dsn" -Atc 'select count(*) from (select site_id,start_ts,session_id,count(*) from sessions group by 1,2,3 having count(*)>1) x')
read -r probe_events probe_visitors probe_sessions <<<"$(psql "$dsn" -At -F' ' -c "select count(*),count(distinct visitor_id),count(distinct session_id) from events where path like '/final-restart-probe-%'")"
mismatches=$(( (events==expected?0:1) + (session_events==expected?0:1) + (duplicate_sessions==0?0:1) + (probe_events==3?0:1) + (probe_visitors==1?0:1) + (probe_sessions==1?0:1) ))
python3 -I - "$ART/meta.json" "$total" "$events" "$session_events" "$duplicate_sessions" "$probe_events" "$probe_visitors" "$probe_sessions" "$mismatches" "$privacy_matches" "$schema" <<'PY'
import json,sys
out,total,events,se,dupes,pe,pv,ps,mis,pm,schema=sys.argv[1:]
json.dump({'generated_events':int(total),'restart_probe_events':int(pe),'stored_events':int(events),'session_event_total':int(se),'duplicate_sessions':int(dupes),'restart_probe_visitors':int(pv),'restart_probe_sessions':int(ps),'truth_mismatches':int(mis),'privacy_matches':int(pm),'signals':['kill -9','SIGTERM'],'isolation':'schema:'+schema,'pass':int(mis)==0 and int(pm)==0},open(out,'w'),indent=2)
PY
cleanup; trap - EXIT
finish complete "$ART/meta.json"
