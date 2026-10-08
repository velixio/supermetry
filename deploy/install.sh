#!/bin/sh
set -eu

usage() {
    cat <<'EOF'
Usage: install.sh [--yes] [--release-url URL] [--version VERSION]

Download and install a Panel Linux release. PANEL_RELEASE_URL is required
unless --release-url is supplied. The URL must be the release asset base and
normally ends in VERSION. PANEL_VERSION or --version overrides that final
path component.
EOF
}

die() {
    echo "install.sh: $*" >&2
    exit 1
}

yes=0
release_url=${PANEL_RELEASE_URL:-}
version=${PANEL_VERSION:-}

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

[ -n "$release_url" ] || die "set PANEL_RELEASE_URL or pass --release-url"
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

archive=panel_${version}_linux_${arch}.tar.gz
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
[ -f "$tmpdir/unpack/panel" ] || die "release archive does not contain panel"
[ -f "$tmpdir/unpack/panel.service" ] || die "release archive does not contain panel.service"
[ -f "$tmpdir/unpack/LICENSE" ] || die "release archive does not contain LICENSE"
[ -f "$tmpdir/unpack/NOTICE" ] || die "release archive does not contain NOTICE"

if ! grep -q '^panel:' /etc/group; then
    if command -v groupadd >/dev/null 2>&1; then
        groupadd --system panel
    elif command -v addgroup >/dev/null 2>&1; then
        addgroup -S panel
    else
        die "groupadd or addgroup is required"
    fi
fi

if ! id panel >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then
        useradd --system --gid panel --home-dir /var/lib/panel \
            --shell /usr/sbin/nologin panel
    elif command -v adduser >/dev/null 2>&1; then
        adduser -S -D -H -G panel -h /var/lib/panel -s /sbin/nologin panel
    else
        die "useradd or adduser is required"
    fi
fi

install -d -m 0700 -o panel -g panel /var/lib/panel
install -d -m 0750 -o root -g panel /etc/panel
install -m 0755 "$tmpdir/unpack/panel" /usr/local/bin/panel
install -m 0644 "$tmpdir/unpack/panel.service" /etc/systemd/system/panel.service

if [ ! -e /etc/panel/panel.env ]; then
    umask 077
    cat >/etc/panel/panel.env <<'EOF'
PANEL_DATABASE_URL=
PANEL_SECRET_KEY=
PANEL_BASE_URL=
PANEL_LISTEN=127.0.0.1:8787
PANEL_TRUSTED_PROXIES=
EOF
    chown root:panel /etc/panel/panel.env
fi

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload
fi

cat <<'EOF'
Panel is installed.

Next steps:
  1. Set PANEL_DATABASE_URL, PANEL_SECRET_KEY, and PANEL_BASE_URL in
     /etc/panel/panel.env.
  2. If using a reverse proxy, set PANEL_TRUSTED_PROXIES to its IP/CIDR.
  3. Run: systemctl enable --now panel
  4. Check: systemctl status panel
EOF
