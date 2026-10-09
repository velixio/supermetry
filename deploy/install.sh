#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
Usage: install.sh [--yes] [--release-url URL] [--version VERSION]

Download and install a Supermetry Linux release. SUPERMETRY_RELEASE_URL is required
unless --release-url is supplied. The URL must be the release asset base and
normally ends in VERSION. SUPERMETRY_VERSION or --version overrides that final
path component.
EOF
}

die() {
    echo "install.sh: $*" >&2
    exit 1
}

yes=0
release_url=${SUPERMETRY_RELEASE_URL:-}
version=${SUPERMETRY_VERSION:-}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --yes)
            yes=1
            shift
            ;;
        --release-url)
            [ "$#" -ge 2 ] || die "--release-url requires a value"
            release_url=$2
            shift 2
            ;;
        --version)
            [ "$#" -ge 2 ] || die "--version requires a value"
            version=$2
            shift 2
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            die "unknown argument: $1"
            ;;
    esac
done

if [ ! -t 0 ] && [ "$yes" -ne 1 ]; then
    die "refusing non-interactive or piped installation without --yes"
fi

[ -n "$release_url" ] || die "set SUPERMETRY_RELEASE_URL or pass --release-url"
release_url=${release_url%/}
if [ -z "$version" ]; then
    version=${release_url##*/}
fi
case "$version" in
    ""|*[!A-Za-z0-9._+-]*)
        die "invalid release version: $version"
        ;;
esac

[ "$(uname -s)" = "Linux" ] || die "only Linux is supported"
case "$(uname -m)" in
    x86_64|amd64)
        arch=amd64
        ;;
    aarch64|arm64)
        arch=arm64
        ;;
    *)
        die "unsupported architecture: $(uname -m)"
        ;;
esac

[ "$(id -u)" -eq 0 ] || die "run as root"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"
command -v tar >/dev/null 2>&1 || die "tar is required"
command -v install >/dev/null 2>&1 || die "install is required"

download() {
    url=$1
    output=$2
    if command -v curl >/dev/null 2>&1; then
        curl -fL --retry 3 --output "$output" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -O "$output" "$url"
    else
        die "curl or wget is required"
    fi
}

archive=supermetry_${version}_linux_${arch}.tar.gz
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM

download "$release_url/$archive" "$tmpdir/$archive"
download "$release_url/SHA256SUMS" "$tmpdir/SHA256SUMS"

awk -v file="$archive" '$2 == file || $2 == "*" file { print }' \
    "$tmpdir/SHA256SUMS" >"$tmpdir/SHA256SUMS.selected"
[ "$(wc -l <"$tmpdir/SHA256SUMS.selected" | tr -d ' ')" -eq 1 ] ||
    die "checksum entry for $archive is missing or ambiguous"
(
    cd "$tmpdir"
    sha256sum -c SHA256SUMS.selected
)

mkdir "$tmpdir/unpack"
tar -xzf "$tmpdir/$archive" -C "$tmpdir/unpack"
[ -f "$tmpdir/unpack/supermetry" ] || die "release archive does not contain supermetry"
[ -f "$tmpdir/unpack/supermetry.service" ] || die "release archive does not contain supermetry.service"
[ -f "$tmpdir/unpack/LICENSE" ] || die "release archive does not contain LICENSE"
[ -f "$tmpdir/unpack/NOTICE" ] || die "release archive does not contain NOTICE"

if ! grep -q '^supermetry:' /etc/group; then
    if command -v groupadd >/dev/null 2>&1; then
        groupadd --system supermetry
    elif command -v addgroup >/dev/null 2>&1; then
        addgroup -S supermetry
    else
        die "groupadd or addgroup is required"
    fi
fi

if ! id supermetry >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then
        useradd --system --gid supermetry --home-dir /var/lib/supermetry \
            --shell /usr/sbin/nologin supermetry
    elif command -v adduser >/dev/null 2>&1; then
        adduser -S -D -H -G supermetry -h /var/lib/supermetry -s /sbin/nologin supermetry
    else
        die "useradd or adduser is required"
    fi
fi

install -d -m 0700 -o supermetry -g supermetry /var/lib/supermetry
install -d -m 0750 -o root -g supermetry /etc/supermetry
install -m 0755 "$tmpdir/unpack/supermetry" /usr/local/bin/supermetry
install -m 0644 "$tmpdir/unpack/supermetry.service" /etc/systemd/system/supermetry.service

if [ ! -e /etc/supermetry/supermetry.env ]; then
    umask 077
    cat >/etc/supermetry/supermetry.env <<'EOF'
SUPERMETRY_DATABASE_URL=
SUPERMETRY_SECRET_KEY=
SUPERMETRY_BASE_URL=
SUPERMETRY_LISTEN=127.0.0.1:8787
SUPERMETRY_TRUSTED_PROXIES=

# Session replay: off unless true; this is the only switch and applies to every site.
SUPERMETRY_REPLAY_ENABLED=false
# Optional; when set, each wins over the dashboard (see .env.example for all keys).
# A custom SUPERMETRY_REPLAY_DIR must stay under /var/lib/supermetry (systemd ReadWritePaths).
#SUPERMETRY_REPLAY_SAMPLE_RATE=15
#SUPERMETRY_REPLAY_RETENTION_DAYS=30
#SUPERMETRY_REPLAY_QUOTA=5GB
#SUPERMETRY_REPLAY_DIR=/var/lib/supermetry/replay
EOF
    chown root:supermetry /etc/supermetry/supermetry.env
fi

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
fi

cat <<'EOF'
Supermetry is installed.

Next steps:
  1. Set SUPERMETRY_DATABASE_URL, SUPERMETRY_SECRET_KEY, and SUPERMETRY_BASE_URL in
     /etc/supermetry/supermetry.env.
  2. If using a reverse proxy, set SUPERMETRY_TRUSTED_PROXIES to its IP/CIDR.
  3. Optional: set SUPERMETRY_REPLAY_ENABLED=true to record session replays.
  4. Run: systemctl enable --now supermetry
  5. Check: systemctl status supermetry
EOF
