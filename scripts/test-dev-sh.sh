#!/bin/sh
# Smoke test for scripts/dev.sh using a fake build, a fake server and a fake Vite
# (no Go, database or Bun). Checks: build+start, rebuild+restart on a change, a
# failed build keeps the old server, add/rename/delete triggers, ignored files,
# Vite dying stops everything, and SIGTERM/SIGINT leave no process behind.
# Needs setsid and python3 (test harness only; dev.sh itself needs neither);
# the test skips with a message when they are missing.
set -eu

command -v setsid >/dev/null 2>&1 && command -v python3 >/dev/null 2>&1 || {
	echo "SKIP: setsid and python3 are required by this test"
	exit 0
}

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
root=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-dev-test.XXXXXX")
pids=$root/pids # every process the test starts records its pid here
: >"$pids"
kill_all() { for p in $(cat "$pids" 2>/dev/null); do kill -KILL "$p" 2>/dev/null || true; done; }
trap 'kill_all; rm -rf "$root"' EXIT HUP INT TERM
fail() { echo "FAIL: $*"; echo "--- dev.sh log ---"; cat "$root/log" 2>/dev/null || true; exit 1; }

mkdir -p "$root/cmd" "$root/internal/x" "$root/web"
echo 1 >"$root/cmd/main.go"
echo 'module x' >"$root/go.mod"
: >"$root/go.sum"
cp "$repo_root/scripts/dev.sh" "$root/dev.sh"

# Fake build: fails when a source contains BROKEN, otherwise writes a fake server
# that reports the version found in cmd/main.go and records its pid.
cat >"$root/build.sh" <<B
if grep -rq BROKEN cmd internal; then echo "main.go:1: compile error"; exit 1; fi
v=\$(cat cmd/main.go)
printf '#!/bin/sh\ntrap "exit 0" TERM INT\necho \$\$ >>$pids\necho "server v%s up pid \$\$"\nwhile :; do sleep 0.1; done\n' "\$v" >"\$OUT"
chmod +x "\$OUT"
B
# Fake Vite: a shell with a sleeping grandchild, like `bun run dev` -> node vite.
cat >"$root/web.sh" <<B
sh -c 'echo \$\$ >>$pids; sleep 300 & echo \$! >>$pids; wait'
B

start() { # start [web command]; sets pid
	web=${1:-". $root/web.sh"}
	(cd "$root" && SUPERMETRY_BASE_URL=http://localhost:8787 SUPERMETRY_DEV_UI_URL=http://localhost:5173 SUPERMETRY_LISTEN=:8787 DEV_POLL=0.2 DEV_BIN="$root/bin/supermetry" \
		DEV_BUILD_CMD='. ./build.sh' DEV_WEB_CMD="$web" \
		DEV_SERVER_CMD="exec $root/bin/supermetry" \
		setsid python3 -c 'import os, signal, sys
signal.signal(signal.SIGINT, signal.SIG_DFL)  # background jobs start with SIGINT ignored
os.execvp("sh", ["sh"] + sys.argv[1:])' "$root/dev.sh" >"$root/log" 2>&1 &
		echo $! >"$root/pid")
	pid=$(cat "$root/pid")
	echo "$pid" >>"$pids"
}
wait_for() { # wait_for <pattern> <description>
	i=0
	while ! grep -q "$1" "$root/log"; do
		i=$((i + 1)); [ "$i" -le 100 ] || fail "timed out waiting for: $2"; sleep 0.1
	done
	echo "ok: $2"
}
alive() { for p in $(cat "$pids"); do kill -0 "$p" 2>/dev/null && echo "$p"; done || true; }
wait_gone() { # wait_gone <description>: every recorded process must be gone
	i=0; while [ -n "$(alive)" ] && [ "$i" -lt 60 ]; do sleep 0.1; i=$((i + 1)); done
	[ -z "$(alive)" ] || fail "processes still alive after $1: $(alive | tr '\n' ' ')"
	echo "ok: no process left after $1"
}
changes() { grep -c 'change detected' "$root/log" || true; }
bump() { sleep 1.1; eval "$1"; } # runs after the mtime tick so -newer sees it

# (a) initial build and start; supervisor, server and fake Vite tree all running
start
wait_for 'server v1 up' 'initial build starts the server (v1)'
wait_for 'Open: http://localhost:8787 ' 'the banner names the single address to open'
wait_for 'Vite runs behind it on http://localhost:5173' 'the banner mentions Vite behind it'
sleep 0.5
[ "$(wc -l <"$pids")" -ge 4 ] || fail "expected supervisor, server, vite shell and its child in $pids"
first=$(sed -n 's/.*server v1 up pid \([0-9]*\).*/\1/p' "$root/log" | head -1)

# (b) touching a source rebuilds and restarts
bump "echo 2 >$root/cmd/main.go"
wait_for 'server v2 up' 'editing a .go file restarts the server (v2)'
kill -0 "$first" 2>/dev/null && fail "old server pid $first is still alive"
echo "ok: old server (pid $first) is gone"

# (c) compile error keeps the running server; the next good edit recovers
bump "echo BROKEN >$root/cmd/main.go"
wait_for 'build failed; keeping' 'a compile error keeps the old server'
wait_for 'compile error' 'the compiler output is shown'
second=$(sed -n 's/.*server v2 up pid \([0-9]*\).*/\1/p' "$root/log" | head -1)
kill -0 "$second" 2>/dev/null || fail "server v2 died after a failed build"
echo "ok: server v2 (pid $second) still running"
bump "echo 3 >$root/cmd/main.go"
wait_for 'server v3 up' 'fixing the error restarts the server (v3)'

# adding an embedded asset, renaming and deleting a Go file each rebuild
bump "echo 4 >$root/cmd/main.go; echo data >$root/internal/x/asset.min.js"
wait_for 'server v4 up' 'adding an embedded asset restarts the server (v4)'
n=$(changes)
bump "mv $root/internal/x/asset.min.js $root/internal/x/renamed.min.js"
i=0; while [ "$(changes)" -le "$n" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
[ "$(changes)" -gt "$n" ] || fail "renaming a source did not trigger a rebuild"
echo "ok: renaming a source triggers a rebuild"
sleep 1; n=$(changes)
bump "rm $root/internal/x/renamed.min.js"
i=0; while [ "$(changes)" -le "$n" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
[ "$(changes)" -gt "$n" ] || fail "deleting a source did not trigger a rebuild"
echo "ok: deleting a source triggers a rebuild"

# web/dist and test files are ignored
sleep 1; n=$(changes); mkdir -p "$root/web/dist"
bump "echo x >$root/web/dist/a.js; echo x >$root/internal/x/a_test.go"; sleep 1
[ "$(changes)" -eq "$n" ] || fail "ignored files triggered a rebuild"
echo "ok: web/dist and _test.go changes are ignored"

# (d) SIGTERM to the supervisor
alive_before=$(alive | wc -l); [ "$alive_before" -ge 4 ] || fail "expected running processes"
kill -TERM "$pid"
wait_gone SIGTERM

# SIGINT to the supervisor only (not the group) also cleans up
: >"$pids"; start
wait_for 'server v4 up' 'second run starts'
sleep 0.5
kill -INT "$pid"
wait_gone SIGINT

# Vite dying at startup (for example :5173 busy) stops the whole run
: >"$pids"; start "echo 'Port 5173 is in use'; exit 1"
wait_for 'vite exited' 'a dying Vite is reported'
wait_gone 'Vite exits'
# Behind a domain the banner opens SUPERMETRY_BASE_URL and names the allowed Vite host.
for b in https://Ana.VoiceStudio.sh:8443/x/ http://localhost:8787; do
	(cd "$root" && SUPERMETRY_BASE_URL=$b DEV_POLL=0.2 DEV_BIN="$root/bin/supermetry" DEV_BUILD_CMD='. ./build.sh' \
		DEV_WEB_CMD="exec sleep 5" DEV_SERVER_CMD="exec $root/bin/supermetry" timeout 3 sh "$root/dev.sh") >"$root/log" 2>&1 || true
	if [ "$b" = http://localhost:8787 ]; then
		grep -q 'Open: http://localhost:8787 ' "$root/log" && ! grep -q 'Host:' "$root/log" || fail "banner for a local base URL"
	else
		grep -q 'Open: https://Ana.VoiceStudio.sh:8443/x  ' "$root/log" || fail "banner Open line for a domain"
		grep -q 'Host:  ana.voicestudio.sh is allowed for Vite (from SUPERMETRY_BASE_URL)' "$root/log" || fail "banner Host line for a domain"
	fi
done
echo "ok: the banner opens SUPERMETRY_BASE_URL and names the allowed host for a domain, nothing extra for localhost"

# make dev binds to loopback unless SUPERMETRY_LISTEN is set, and the banner says localhost.
make -C "$repo_root" -n dev 2>/dev/null | grep -q 'if \[ -z "\$SUPERMETRY_LISTEN" \]; then export SUPERMETRY_LISTEN=127.0.0.1:8787' || fail "make dev does not default SUPERMETRY_LISTEN to loopback"
echo "ok: make dev defaults SUPERMETRY_LISTEN to 127.0.0.1:8787 when unset"
for l in 127.0.0.1:8787 0.0.0.0:8787; do
	: >"$pids"
	(cd "$root" && SUPERMETRY_LISTEN=$l DEV_POLL=0.2 DEV_BIN="$root/bin/supermetry" DEV_BUILD_CMD='. ./build.sh' \
		DEV_WEB_CMD="exec sleep 5" DEV_SERVER_CMD="exec $root/bin/supermetry" timeout 3 sh "$root/dev.sh") >"$root/log" 2>&1 || true
	grep -q 'Open: http://localhost:8787 ' "$root/log" || fail "banner for SUPERMETRY_LISTEN=$l"
	for p in $(cat "$pids"); do kill "$p" 2>/dev/null || true; done
done
echo "ok: the banner shows localhost for loopback and wildcard listen addresses"

# Missing Bun: a clear message, a failing exit status and nothing left running.
if [ -z "$(PATH=/usr/bin:/bin command -v bun || true)" ]; then
	: >"$pids"
	rc=0
	(cd "$root" && PATH=/usr/bin:/bin DEV_BIN="$root/bin/supermetry" DEV_BUILD_CMD='. ./build.sh' \
		DEV_SERVER_CMD="exec $root/bin/supermetry" sh "$root/dev.sh") >"$root/log" 2>&1 || rc=$?
	[ "$rc" -ne 0 ] || fail "dev.sh succeeded without bun"
	grep -q 'bun is required: https://bun.sh' "$root/log" || fail "missing the bun message"
	echo "ok: a missing bun stops with a clear message (exit $rc)"
	wait_gone 'a missing bun'
else
	echo "skip: bun is on the default PATH, cannot test the missing-bun path"
fi
echo "PASS"
