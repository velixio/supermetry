#!/usr/bin/env bash
# Run 1 crashes the main process with process.crash(); run 2 starts again on the
# same userData so the SDK finds the minidump and sends it as an attachment.
# Both runs use an empty environment (env -i) so the dump's stack memory holds
# no machine data; only HOME, PATH, the userData path and the DSN are passed.
# Scenario consent-denied: Sentry.init with beforeSend returning null; run 1
# crashes, run 2 must send nothing.
set -u
src="$(cd "$(dirname "$0")" && pwd)"
# Run from a neutral path so the dump holds no checkout path.
app=/tmp/fixture-app
rm -rf "$app"; cp -r "$src" "$app"
cd "$app" || exit 1
unset ELECTRON_RUN_AS_NODE
scenario="${1:-native-crash}"
case "$scenario" in
  native-crash) first=native-crash-1 second=native-crash-2 ;;
  consent-denied) first=consent-denied-1 second=consent-denied-2 ;;
  *) echo "unknown scenario $scenario" >&2; exit 2 ;;
esac
ud="$(mktemp -d)"
home=/tmp/fixture-home
mkdir -p "$home"
run() {
  env -i HOME="$home" PATH=/usr/bin:/bin FIXTURE_USERDATA="$ud" \
    SENTRY_FIXTURE_DSN="${SENTRY_FIXTURE_DSN:-}" \
    timeout 40 xvfb-run -a node_modules/.bin/electron . --no-sandbox "$1" >/dev/null 2>&1
}
run "$first"
run "$second"
rm -rf "${ud:?}" "$home" "$app"
