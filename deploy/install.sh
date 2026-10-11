#!/bin/sh
set -eu

die() {
    echo "install.sh: $*" >&2
    exit 1
}

version=${SUPERMETRY_VERSION:-latest}
yes=0
expect_version=0

for arg in "$@"; do
    if [ "$expect_version" -eq 1 ]; then
        version=$arg
        expect_version=0
        continue
    fi
    case "$arg" in
        --yes)
            yes=1
            ;;
        --version)
            expect_version=1
            ;;
        --version=*)
            version=${arg#--version=}
            ;;
    esac
done
[ "$expect_version" -eq 0 ] || die "--version requires a value"

if [ ! -t 0 ] && ! (: </dev/tty) 2>/dev/null && [ "$yes" -ne 1 ]; then
    die "no terminal for questions; pass --yes with the settings as flags (see supermetry install --help)"
fi

case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) die "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
esac

download() {
    url=$1
    output=$2
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --retry 3 -o "$output" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -q -O "$output" "$url"
    else
        die "curl or wget is required"
    fi
}

resolve_latest_version() {
    latest_url=https://github.com/velixio/supermetry/releases/latest
    if command -v curl >/dev/null 2>&1; then
        effective_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$latest_url")
    elif command -v wget >/dev/null 2>&1; then
        effective_url=$(
            wget --max-redirect=10 --server-response --spider "$latest_url" 2>&1 |
                awk '/^[[:space:]]*Location:/ { url=$2 } END { sub(/\r$/, "", url); print url }'
        )
    else
        die "curl or wget is required"
    fi
    resolved=${effective_url##*/}
    resolved=${resolved#v}
    [ -n "$resolved" ] || die "could not resolve the latest release version"
    printf '%s\n' "$resolved"
}

if [ "$version" = latest ]; then
    version=$(resolve_latest_version)
    release_url=https://github.com/velixio/supermetry/releases/latest/download
else
    version=${version#v}
    release_url=https://github.com/velixio/supermetry/releases/download/v${version}
fi
release_url=${SUPERMETRY_RELEASE_URL:-$release_url}
release_url=${release_url%/}

case "$version" in
    ""|*[!A-Za-z0-9._+-]*) die "invalid release version: $version" ;;
esac

archive=supermetry_${version}_${os}_${arch}.tar.gz
tmp=$(mktemp -d "${TMPDIR:-/tmp}/supermetry-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

download "$release_url/$archive" "$tmp/$archive"
download "$release_url/SHA256SUMS" "$tmp/SHA256SUMS"

awk -v file="$archive" '$2 == file || $2 == "*" file { print }' \
    "$tmp/SHA256SUMS" >"$tmp/SHA256SUMS.selected"
[ "$(wc -l <"$tmp/SHA256SUMS.selected" | tr -d ' ')" -eq 1 ] ||
    die "checksum entry for $archive is missing or ambiguous"

if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && sha256sum -c SHA256SUMS.selected)
elif command -v shasum >/dev/null 2>&1; then
    (cd "$tmp" && shasum -a 256 -c SHA256SUMS.selected)
else
    die "sha256sum or shasum is required"
fi

tar -xzf "$tmp/$archive" -C "$tmp"
[ -x "$tmp/supermetry" ] || die "release archive does not contain an executable supermetry"

if [ ! -t 0 ] && (: </dev/tty) 2>/dev/null; then
    exec "$tmp/supermetry" install "$@" </dev/tty
fi
exec "$tmp/supermetry" install "$@"
