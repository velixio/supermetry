#!/bin/sh
# Development supervisor behind `make dev`. Run it from the repo root with the
# server environment already exported (the Makefile does that, see DB_ENV).
#
#   1. Builds bin/supermetry (no embedui tag; the UI comes from Vite) and runs it.
#   2. Polls the Go sources about once a second. On a change it rebuilds and, only
#      if the build succeeded, restarts the server. A compile error is printed and
#      the old server keeps running until the next change.
#   3. Runs the Vite dev server (hot reload on :5173, proxying to the Go server).
#   4. SIGINT/SIGTERM stop both children and wait for them, leaving no orphans.
#
# Overrides (used by scripts/test-dev-sh.sh): DEV_BUILD_CMD (receives $OUT, the
# output path), DEV_SERVER_CMD, DEV_WEB_CMD (empty disables Vite), DEV_POLL
# (seconds), DEV_WATCH (space separated paths).
set -u

bin=${DEV_BIN:-$PWD/bin/supermetry}
poll=${DEV_POLL:-1}
watch=${DEV_WATCH:-cmd internal web go.mod go.sum}
build_cmd=${DEV_BUILD_CMD:-go build -o "\$OUT" ./cmd/supermetry}
server_cmd=${DEV_SERVER_CMD:-exec $bin serve}
web_cmd=${DEV_WEB_CMD-cd web && bun install --frozen-lockfile >/dev/null && exec bun run dev}

mkdir -p "$(dirname "$bin")"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-dev.XXXXXX") || exit 1
stamp=$tmp/stamp
server_pid=
web_pid=

log() { printf '[dev] %s\n' "$*"; }

# Source files the Go build depends on: Go code, go.mod/go.sum and the assets
# embedded from cmd/ and internal/. web/ contributes only its Go files. Extra
# arguments are find predicates (used for -newer).
sources() {
	# shellcheck disable=SC2086
	find $watch -type f "$@" -not -name '*_test.go' \
		\( -path 'web/*' -name '*.go' -o -not -path 'web/*' \) \
		-not -path '*/node_modules/*' -not -path 'web/dist/*' 2>/dev/null
}

# Succeeds when a source was modified or added since the last call, or when the
# number of sources changed (a deletion). Cheap: one find per second, no forks per file.
count=0
changed() {
	touch "$tmp/next"
	n=$(sources | wc -l)
	newer=$(sources -newer "$stamp" | head -n 1)
	mv -f "$tmp/next" "$stamp"
	[ -n "$newer" ] || [ "$n" != "$count" ] && { count=$n; return 0; }
	return 1
}

stop_server() {
	[ -n "$server_pid" ] || return 0
	if kill -0 "$server_pid" 2>/dev/null; then
		kill -TERM "$server_pid" 2>/dev/null
		# The server shuts down gracefully (up to 10s); then force it.
		i=0
		while kill -0 "$server_pid" 2>/dev/null && [ "$i" -lt 150 ]; do
			sleep 0.1
			i=$((i + 1))
		done
		kill -0 "$server_pid" 2>/dev/null && kill -KILL "$server_pid" 2>/dev/null
	fi
	# Reap it: once wait returns, the process is gone and its port is free.
	wait "$server_pid" 2>/dev/null
	server_pid=
}

start_server() {
	sh -c "$server_cmd" &
	server_pid=$!
	log "server started (pid $server_pid)"
}

# Builds into a temp file and swaps it in only on success, so a failed build never
# touches the binary the running server was started from.
build() {
	OUT=$bin.new
	export OUT
	if sh -c "$build_cmd"; then
		mv -f "$bin.new" "$bin"
		return 0
	fi
	rm -f "$bin.new"
	return 1
}

# Signals a process and all its descendants, deepest first. Needed because
# `bun run dev` starts Vite as a grandchild that would otherwise outlive us.
kill_tree() {
	for child in $(pgrep -P "$1" 2>/dev/null); do kill_tree "$child"; done
	kill -TERM "$1" 2>/dev/null
}

cleanup() {
	trap '' INT TERM HUP EXIT
	log "stopping"
	if [ -n "$web_pid" ]; then
		kill_tree "$web_pid"
		wait "$web_pid" 2>/dev/null
	fi
	stop_server
	rm -rf "$tmp"
}
trap 'cleanup; exit 0' INT TERM HUP
trap 'cleanup' EXIT

if build; then
	start_server
else
	log "initial build failed; fix the error and save, the server starts on the next change"
fi
if [ -n "$web_cmd" ]; then
	sh -c "$web_cmd" &
	web_pid=$!
fi

touch "$stamp"
count=$(sources | wc -l)
log "watching: $watch"
while :; do
	# Interruptible sleep so a signal is handled immediately.
	sleep "$poll" &
	wait $!
	if [ -n "$server_pid" ] && ! kill -0 "$server_pid" 2>/dev/null; then
		wait "$server_pid" 2>/dev/null
		server_pid=
		log "server exited; waiting for a change"
	fi
	changed || continue
	log "change detected, rebuilding"
	if build; then
		stop_server
		start_server
	else
		log "build failed; keeping the previous server running"
	fi
done
