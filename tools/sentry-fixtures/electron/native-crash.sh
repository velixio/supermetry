#!/usr/bin/env bash
# Run 1 crashes the main process with process.crash(); run 2 starts again on the
# same userData so the SDK finds the minidump and sends it as an attachment.
set -u
cd "$(dirname "$0")"
unset ELECTRON_RUN_AS_NODE
ud="$(mktemp -d)"
export FIXTURE_USERDATA="$ud"
timeout 40 xvfb-run -a node_modules/.bin/electron . --no-sandbox native-crash-1 >/dev/null 2>&1
timeout 40 xvfb-run -a node_modules/.bin/electron . --no-sandbox native-crash-2 >/dev/null 2>&1
rm -rf "${ud:?}"
