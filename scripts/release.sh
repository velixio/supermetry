#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="${SUPERMETRY_RELEASE_VERSION:-$(git describe --tags --always)}"
version="${version#v}"
targets="${SUPERMETRY_RELEASE_TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64}"

if [[ ! "$version" =~ ^[A-Za-z0-9._+-]+$ ]]; then
    echo "release.sh: unsafe version for archive name: $version" >&2
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
    -name SHA256SUMS -o \
    -name install.sh -o \
    -name install.ps1 \
\) -delete

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

for target in $targets; do
    goos="${target%/*}"
    goarch="${target#*/}"
    case "$goos/$goarch" in
        linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64|windows/arm64)
            ;;
        *)
            echo "release.sh: unsupported target: $target" >&2
            exit 1
            ;;
    esac

    package_dir="$workdir/supermetry_${version}_${goos}_${goarch}"
    mkdir -p "$package_dir"

    echo "Building supermetry $version for $goos/$goarch"
    binary=supermetry
    if [[ "$goos" == windows ]]; then
        binary=supermetry.exe
    fi
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -tags embedui -trimpath \
        -ldflags "-s -w -X main.version=$version" \
        -o "$package_dir/$binary" ./cmd/supermetry

    cp LICENSE NOTICE "$package_dir/"
    if [[ "$goos" == windows ]]; then
        archive="supermetry_${version}_${goos}_${goarch}.zip"
        zip -q -j "dist/$archive" \
            "$package_dir/$binary" "$package_dir/LICENSE" "$package_dir/NOTICE"
    else
        cp deploy/supermetry.service "$package_dir/"
        archive="supermetry_${version}_${goos}_${goarch}.tar.gz"
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
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "${archives[@]}" >SHA256SUMS
    else
        shasum -a 256 "${archives[@]}" >SHA256SUMS
    fi
)

echo "Release artifacts written to $repo_root/dist"
