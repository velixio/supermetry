#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="${SUPERMETRY_RELEASE_VERSION:-$(git describe --tags --always)}"
version="${version#v}"
targets="${SUPERMETRY_RELEASE_TARGETS:-linux/amd64 linux/arm64 linux/armv6 linux/armv7 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64}"
release_signing_public_key="${RELEASE_SIGNING_PUBLIC_KEY:-}"
release_trusted_public_keys="${RELEASE_TRUSTED_PUBLIC_KEYS:-$release_signing_public_key}"

if [[ ! "$version" =~ ^[A-Za-z0-9._+-]+$ ]]; then
    echo "release.sh: unsafe version for archive name: $version" >&2
    exit 1
fi
if [[ ! "$release_signing_public_key" =~ ^[A-Za-z0-9+/]{43}=$ ]]; then
    echo "release.sh: RELEASE_SIGNING_PUBLIC_KEY must be standard base64 for a 32-byte Ed25519 public key" >&2
    exit 1
fi
IFS=',' read -r -a trusted_public_keys <<< "$release_trusted_public_keys"
if ((${#trusted_public_keys[@]} == 0)); then
    echo "release.sh: RELEASE_TRUSTED_PUBLIC_KEYS must contain at least one public key" >&2
    exit 1
fi
active_key_is_trusted=0
for trusted_public_key in "${trusted_public_keys[@]}"; do
    if [[ ! "$trusted_public_key" =~ ^[A-Za-z0-9+/]{43}=$ ]]; then
        echo "release.sh: RELEASE_TRUSTED_PUBLIC_KEYS must be comma-separated standard-base64 Ed25519 public keys" >&2
        exit 1
    fi
    if [[ "$trusted_public_key" == "$release_signing_public_key" ]]; then
        active_key_is_trusted=1
    fi
done
if ((active_key_is_trusted == 0)); then
    echo "release.sh: RELEASE_TRUSTED_PUBLIC_KEYS must include RELEASE_SIGNING_PUBLIC_KEY" >&2
    exit 1
fi

command -v bun >/dev/null 2>&1 || {
    echo "release.sh: bun is required" >&2
    exit 1
}
command -v go >/dev/null 2>&1 || {
    echo "release.sh: go is required" >&2
    exit 1
}

(
    cd web
    bun run build
)

mkdir -p dist
find dist -maxdepth 1 -type f \( \
    -name 'supermetry_*.tar.gz' -o \
    -name 'supermetry_*.zip' -o \
    -name 'supermetry_*_*_*' -o \
    -name SHA256SUMS -o \
    -name install.sh -o \
    -name install.ps1 \
\) -delete

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

for target in $targets; do
    goos="${target%/*}"
    release_arch="${target#*/}"
    goarch="$release_arch"
    goarm=""
    case "$goos/$release_arch" in
        linux/armv6)
            goarch=arm
            goarm=6
            ;;
        linux/armv7)
            goarch=arm
            goarm=7
            ;;
        linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64)
            ;;
        *)
            echo "release.sh: unsupported target: $target" >&2
            exit 1
            ;;
    esac

    package_dir="$workdir/supermetry_${version}_${goos}_${release_arch}"
    mkdir -p "$package_dir"

    echo "Building supermetry $version for $goos/$release_arch"
    binary=supermetry
    if [[ "$goos" == windows ]]; then
        binary=supermetry.exe
    fi
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
        go build -tags embedui -trimpath \
        -ldflags "-s -w -X main.version=$version -X main.releaseArch=$release_arch -X main.releaseTrustedPublicKeys=$release_trusted_public_keys" \
        -o "$package_dir/$binary" ./cmd/supermetry

    bare="supermetry_${version}_${goos}_${release_arch}"
    if [[ "$goos" == windows ]]; then
        bare="$bare.exe"
    fi
    cp "$package_dir/$binary" "dist/$bare"

    cp LICENSE NOTICE "$package_dir/"
    if [[ "$goos" == windows ]]; then
        archive="supermetry_${version}_${goos}_${release_arch}.zip"
        zip -q -j "dist/$archive" \
            "$package_dir/$binary" "$package_dir/LICENSE" "$package_dir/NOTICE"
    else
        cp deploy/supermetry.service "$package_dir/"
        archive="supermetry_${version}_${goos}_${release_arch}.tar.gz"
        tar -czf "dist/$archive" -C "$package_dir" \
            "$binary" LICENSE NOTICE supermetry.service
    fi
done

cp deploy/install.sh deploy/install.ps1 dist/

(
    cd dist
    shopt -s nullglob
    archives=(supermetry_*.tar.gz supermetry_*.zip)
    if ((${#archives[@]} == 0)); then
        echo "release.sh: no release archives were built" >&2
        exit 1
    fi
    files=(supermetry_*)
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "${files[@]}" >SHA256SUMS
    else
        shasum -a 256 "${files[@]}" >SHA256SUMS
    fi
)

echo "Release artifacts written to $repo_root/dist"
