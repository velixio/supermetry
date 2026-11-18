#!/usr/bin/env bash
# Live debug file upload acceptance: start a fresh Supermetry and run the exact
# sentry-cli command lines the Sentry Gradle plugin and the Xcode build phase
# run (the plugin and Xcode shell out to sentry-cli; Gradle and Xcode
# themselves are not run here), then check that every file is listed by the
# Symbols API with a usable cache.
# Run under the heavy lock:
#   flock /tmp/claude-1000/supermetry-heavy.lock make sdk-compat-debugfiles
# Command shapes, from the plugin and sample app sources:
#   Gradle ProGuard   sentry-cli upload-proguard --uuid <uuid> <mapping.txt>
#                     (SentryUploadProguardMappingsTask.getArguments)
#   Gradle native     sentry-cli debug-files upload <build/intermediates/merged_native_libs/<variant>>
#                     (SentryUploadNativeSymbolsTask.getArguments; --include-sources when enabled)
#   Xcode build phase sentry-cli debug-files upload --include-sources "$DWARF_DSYM_FOLDER_PATH"
#                     (sentry-cocoa TestApps/iOS-Swift/iOS-Swift.yml)
# The tool sees only SENTRY_URL, SENTRY_AUTH_TOKEN and SENTRY_PROJECT (the
# plugin's `url` and token settings, the Xcode phase's environment); the org
# comes from the sntrys_ token as it does for the plugin.
# sentry-cli 3.8.2 comes from the npm package's locked, hash-pinned platform
# binary (the version the Gradle plugin bundles); nothing is vendored.
set -uo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd)"
here="$root/tools/sentry-fixtures/live"
app="$here/debugfiles"
. "$here/lib.sh"
sm_init
fail=0; skip=0; pass=0
results=()
cleanup() { sm_stop; rm -rf "$app/node_modules" "${work:?}"; }
trap cleanup EXIT

record() { # name status detail
  results+=("$(printf '%-6s %-46s %s' "$2" "$1" "$3")")
  case "$2" in PASS) pass=$((pass+1));; SKIP) skip=$((skip+1));; *) fail=$((fail+1));; esac
}
check() { # name cmd...
  local name="$1" out rc; shift
  out="$("$@" 2>&1)"; rc=$?
  case $rc in 0) record "$name" PASS "$out";; 3) record "$name" SKIP "$out";; *) record "$name" FAIL "$out";; esac
}

df -h / | tail -1; free -h | sed -n 2p
echo "== deps"
(cd "$app" && bun install --frozen-lockfile >/dev/null) || exit 1
(cd "$root" && go build -o "$work/supermetry" ./cmd/supermetry) || exit 1
cli="$app/node_modules/.bin/sentry-cli"
version="sentry-cli $("$cli" --version 2>&1 | awk '{print $NF}')"
echo "tool: $version"

echo "== server"
sm_start "$work/supermetry" SUPERMETRY_SENTRY_IP_PER_MIN=100000 || exit 1
out="$(python3 -I "$here/check.py" uploadtoken "$base" "$state" debugfiles)" || { echo "site setup: $out"; exit 1; }
read -r site key token <<<"$out"

# Inputs: the committed native fixtures (see tools/sentry-fixtures/native) and the
# ProGuard mapping of the Android fixture.
td="$root/internal/debugfile/testdata"
mkdir -p "$work/in/gradle-native/arm64-v8a" "$work/in/xcode/Fix.app.dSYM/Contents/Resources/DWARF" "$work/in/linux" "$work/in/src"
cp "$td/libfix.so" "$work/in/gradle-native/arm64-v8a/libfix.so"
cp "$td/fix-darwin-fat" "$work/in/xcode/Fix.app.dSYM/Contents/Resources/DWARF/Fix"
cp "$td/fix-linux-amd64" "$work/in/linux/fix"
cp "$root/tools/sentry-fixtures/native/fixture.c" "$work/in/src/fixture.c"
cp "$root/internal/sentryingest/testdata/android/mapping.txt" "$work/in/mapping.txt"
uuid="6a1b8c0e-3d5f-3e2a-9b47-0c1d2e3f4a5b"

clean_env() { env -i HOME="$home" PATH=/usr/bin:/bin:"$(dirname "$(command -v node)")" \
  SENTRY_URL="$base" SENTRY_AUTH_TOKEN="$token" SENTRY_PROJECT="$site" "$@"; }
run() { # name dir cmd...: run the tool, record its exit status
  local name="$1" dir="$2" out rc; shift 2
  out="$(cd "$dir" && clean_env "$@" 2>&1)"; rc=$?
  echo "$out" | tail -6
  if [ $rc -eq 0 ]; then record "$name" PASS "exit 0"; else record "$name" FAIL "exit $rc: $(echo "$out" | tail -2 | tr '\n' ' ')"; fi
}
listed() { # name wait kind:state...
  local name="$1" wait="$2"; shift 2
  check "$name" python3 -I "$here/check.py" debugfiles "$base" "$state" "$site" "$wait" "$@"
}

echo "== sentry-cli info (Gradle plugin org resolution)"
run "info" "$work" "$cli" info

echo "== Gradle ProGuard upload"
run "gradle upload-proguard --uuid" "$work/in" "$cli" upload-proguard --uuid "$uuid" mapping.txt
listed "proguard mapping listed, cache ok" 30 proguard:ok

echo "== Gradle native symbols upload"
run "gradle debug-files upload merged_native_libs" "$work/in/src" "$cli" debug-files upload "$work/in/gradle-native"
listed "elf libfix.so listed, cache ok" 60 elf:ok

echo "== Xcode build phase upload"
run "xcode debug-files upload --include-sources" "$work/in/src" "$cli" debug-files upload --include-sources "$work/in/xcode"
listed "macho slices listed, cache ok" 60 macho:ok

echo "== Linux ELF with DWARF"
run "linux debug-files upload" "$work/in/src" "$cli" debug-files upload "$work/in/linux"
listed "linux elf listed, cache ok" 60 elf:ok

echo "== Source bundle (Gradle includeNativeSources)"
# --include-sources makes the tool build a source bundle from the source files the
# debug info names, found relative to the working directory. Stored, never used
# for symbolication in 2c.
run "gradle debug-files upload --include-sources" "$work/in/src" "$cli" debug-files upload "$work/in/gradle-native" --include-sources
check "source bundle stored" python3 -I "$here/check.py" debugfiles "$base" "$state" "$site" 5 sourcebundle:any

echo "== results ($version)"
printf '%s\n' "${results[@]}"
echo "PASS=$pass SKIP=$skip FAIL=$fail"
[ "$fail" -eq 0 ]
