#!/bin/sh
# Smoke test for scripts/dev.sh using a fake build and a fake server (no Go, no
# database, no Vite). Checks: build+start, rebuild+restart on a change, a failed
# build keeps the old server, deletion/addition triggers, and SIGTERM and SIGINT
# leave no orphan processes.
set -eu

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
root=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-dev-test.XXXXXX")
trap 'pkill -f "$root" 2>/dev/null || true; rm -rf "$root"' EXIT HUP INT TERM
fail() { echo "FAIL: $*"; echo "--- dev.sh log ---"; cat "$root/log" 2>/dev/null || true; exit 1; }

mkdir -p "$root/cmd" "$root/internal/x" "$root/web"
echo 1 >"$root/cmd/main.go"
echo 'module x' >"$root/go.mod"
: >"$root/go.sum"

# Fake build: fails when a source contains BROKEN, otherwise writes a fake server
# that reports the version found in cmd/main.go.
cat >"$root/build.sh" <<'B'
if grep -rq BROKEN cmd internal; then echo "main.go:1: compile error"; exit 1; fi
v=$(cat cmd/main.go)
printf '#!/bin/sh\ntrap "exit 0" TERM INT\necho "server v%s up pid $$"\nwhile :; do sleep 0.1; done\n' "$v" >"$OUT"
chmod +x "$OUT"
B

start() { # start <dev.sh log> ; sets pid
	(cd "$root" && DEV_POLL=0.2 DEV_BIN="$root/bin/supermetry" \
		DEV_BUILD_CMD='. ./build.sh' DEV_WEB_CMD="sh -c 'sleep 300 & wait'" \
		DEV_SERVER_CMD="exec $root/bin/supermetry" \
		setsid python3 -c 'import os, signal, sys
signal.signal(signal.SIGINT, signal.SIG_DFL)  # background jobs start with SIGINT ignored
os.execvp("sh", ["sh"] + sys.argv[1:])' "$repo_root/scripts/dev.sh" >"$root/log" 2>&1 &
		echo $! >"$root/pid")
	pid=$(cat "$root/pid")
}
wait_for() { # wait_for <pattern> <description> [count]
	n=${3:-1}; i=0
	while [ "$(grep -c "$1" "$root/log" || true)" -lt "$n" ]; do
		i=$((i + 1)); [ "$i" -le 100 ] || fail "timed out waiting for: $2"; sleep 0.1
	done
	echo "ok: $2"
}
procs() { pgrep -f "$root" | grep -v "^$$\$" || true; }

# (a) initial build and start
start
wait_for 'server v1 up' 'initial build starts the server (v1)'
first=$(sed -n 's/.*server v1 up pid \([0-9]*\).*/\1/p' "$root/log" | head -1)

# (b) touching a source rebuilds and restarts
sleep 1.1; echo 2 >"$root/cmd/main.go"
wait_for 'server v2 up' 'editing a .go file restarts the server (v2)'
kill -0 "$first" 2>/dev/null && fail "old server pid $first is still alive"
echo "ok: old server (pid $first) is gone"

# (c) compile error keeps the running server; the next good edit recovers
sleep 1.1; echo BROKEN >"$root/cmd/main.go"
wait_for 'build failed; keeping' 'a compile error keeps the old server'
wait_for 'compile error' 'the compiler output is shown'
second=$(sed -n 's/.*server v2 up pid \([0-9]*\).*/\1/p' "$root/log" | head -1)
kill -0 "$second" 2>/dev/null || fail "server v2 died after a failed build"
echo "ok: server v2 (pid $second) still running"
sleep 1.1; echo 3 >"$root/cmd/main.go"
wait_for 'server v3 up' 'fixing the error restarts the server (v3)'

# new file in internal/ and an embedded asset also trigger a rebuild
sleep 1.1; echo 4 >"$root/cmd/main.go"; echo data >"$root/internal/x/asset.min.js"
wait_for 'server v4 up' 'adding an embedded asset restarts the server (v4)'
# test files and web/dist are ignored
mkdir -p "$root/web/dist"
sleep 1.1; echo x >"$root/web/dist/a.js"; echo x >"$root/internal/x/a_test.go"; sleep 1
[ "$(grep -c 'change detected' "$root/log")" -eq 4 ] || fail "ignored files triggered a rebuild"
echo "ok: web/dist and _test.go changes are ignored"

# (d) SIGTERM leaves no orphans
[ -n "$(procs)" ] || fail "expected running processes"
kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid"
i=0; while [ -n "$(procs)" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
[ -z "$(procs)" ] || fail "orphans after SIGTERM: $(procs)"
echo "ok: SIGTERM leaves no orphan processes"

# SIGINT to the supervisor only (not the group) also cleans up
start
wait_for 'server v4 up' 'second run starts'
kill -INT "$pid"
i=0; while [ -n "$(procs)" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
[ -z "$(procs)" ] || fail "orphans after SIGINT: $(procs)"
echo "ok: SIGINT leaves no orphan processes"
echo "PASS"
