#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-install-test.XXXXXX")
trap 'rm -rf "$test_root"' EXIT HUP INT TERM

stub_dir=$test_root/bin
mkdir -p "$stub_dir"

cat >"$stub_dir/uname" <<'EOF'
#!/bin/sh
case "$1" in
    -s) printf '%s\n' "$TEST_UNAME_S" ;;
    -m) printf '%s\n' "$TEST_UNAME_M" ;;
    *) exit 2 ;;
esac
EOF

cat >"$stub_dir/curl" <<'EOF'
#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -o|--output)
            output=$2
            shift 2
            ;;
        -w|--write-out)
            shift 2
            ;;
        -*)
            shift
            ;;
        *)
            url=$1
            shift
            ;;
    esac
done
printf '%s\n' "$url" >>"$TEST_DOWNLOAD_LOG"
if [ -n "$output" ]; then
    case "$url" in
        */SHA256SUMS)
            printf '%s  %s\n' dummy "supermetry_1.2.3_linux_${TEST_EXPECTED_ARCH}.tar.gz" >"$output"
            ;;
        *)
            printf 'downloaded\n' >"$output"
            ;;
    esac
else
    printf '%s\n' 'https://github.com/velixio/supermetry/releases/tag/v1.2.3'
fi
EOF

cat >"$stub_dir/sha256sum" <<'EOF'
#!/bin/sh
if [ "${TEST_CHECKSUM_MISMATCH:-0}" -eq 1 ]; then
    printf '%s\n' 'checksum mismatch' >&2
    exit 1
fi
exit 0
EOF

cat >"$stub_dir/tar" <<'EOF'
#!/bin/sh
destination=
while [ "$#" -gt 0 ]; do
    case "$1" in
        -C)
            destination=$2
            shift 2
            ;;
        *)
            shift
            ;;
    esac
done
cat >"$destination/supermetry" <<'SCRIPT'
#!/bin/sh
printf '%s\n' "$*" >"$TEST_RUN_LOG"
SCRIPT
chmod +x "$destination/supermetry"
EOF

chmod +x "$stub_dir/uname" "$stub_dir/curl" "$stub_dir/sha256sum" "$stub_dir/tar"

fail() {
    printf 'FAIL: %s\n' "$*" >&2
    exit 1
}

run_installer() {
    TEST_UNAME_S=$1 \
    TEST_UNAME_M=$2 \
    TEST_EXPECTED_ARCH=$3 \
    TEST_DOWNLOAD_LOG=$test_root/download.log \
    TEST_RUN_LOG=$test_root/run.log \
    SUPERMETRY_VERSION=1.2.3 \
    SUPERMETRY_RELEASE_URL=https://releases.example.test \
    PATH=$stub_dir:/usr/bin:/bin \
        sh "$repo_root/deploy/install.sh" --yes </dev/null
}

assert_arch() {
    input=$1
    expected=$2
    : >"$test_root/download.log"
    run_installer Linux "$input" "$expected"
    grep -q "/supermetry_1.2.3_linux_${expected}.tar.gz$" "$test_root/download.log" ||
        fail "architecture $input did not map to $expected"
    [ "$(cat "$test_root/run.log")" = "install --yes" ] ||
        fail "downloaded binary did not receive install --yes"
}

assert_arch x86_64 amd64
assert_arch amd64 amd64
assert_arch aarch64 arm64
assert_arch arm64 arm64

set +e
unsupported_output=$(run_installer Linux riscv64 unsupported 2>&1)
unsupported_status=$?
set -e
[ "$unsupported_status" -ne 0 ] || fail "unsupported architecture succeeded"
printf '%s' "$unsupported_output" | grep -q 'unsupported architecture: riscv64' ||
    fail "unsupported architecture message was missing"

set +e
checksum_output=$(
    TEST_CHECKSUM_MISMATCH=1 run_installer Linux amd64 amd64 2>&1
)
checksum_status=$?
set -e
[ "$checksum_status" -ne 0 ] || fail "checksum mismatch succeeded"
printf '%s' "$checksum_output" | grep -q 'checksum mismatch' ||
    fail "checksum mismatch was not reported"

set +e
no_tty_output=$(
    TEST_UNAME_S=Linux \
    TEST_UNAME_M=amd64 \
    TEST_DOWNLOAD_LOG=$test_root/download.log \
    TEST_RUN_LOG=$test_root/run.log \
    SUPERMETRY_VERSION=1.2.3 \
    SUPERMETRY_RELEASE_URL=https://releases.example.test \
    PATH=$stub_dir:/usr/bin:/bin \
        setsid sh "$repo_root/deploy/install.sh" </dev/null 2>&1
)
no_tty_status=$?
set -e
[ "$no_tty_status" -eq 1 ] || fail "closed stdin without --yes exited $no_tty_status, want 1"
expected_message='install.sh: no terminal for questions; pass --yes with the settings as flags (see supermetry install --help)'
printf '%s' "$no_tty_output" | grep -Fq "$expected_message" ||
    fail "closed stdin message was missing"

printf '%s\n' 'install.sh tests passed'
