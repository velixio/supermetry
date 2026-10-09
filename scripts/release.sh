#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="${SUPERMETRY_RELEASE_VERSION:-$(git describe --tags --always)}"
targets="${SUPERMETRY_RELEASE_TARGETS:-linux/amd64 linux/arm64 darwin/arm64}"

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
find dist -maxdepth 1 -type f \( -name 'supermetry_*.tar.gz' -o -name SHA256SUMS \) -delete

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

for target in $targets; do
    goos="${target%/*}"
    goarch="${target#*/}"
    case "$goos/$goarch" in
        linux/amd64|linux/arm64|darwin/arm64)
            ;;
        *)
            echo "release.sh: unsupported target: $target" >&2
            exit 1
            ;;
    esac

    package_dir="$workdir/supermetry_${version}_${goos}_${goarch}"
    mkdir -p "$package_dir"

    echo "Building supermetry $version for $goos/$goarch"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -tags embedui -trimpath \
        -ldflags "-s -w -X main.version=$version" \
        -o "$package_dir/supermetry" ./cmd/supermetry

    cp LICENSE NOTICE deploy/supermetry.service "$package_dir/"
    archive="supermetry_${version}_${goos}_${goarch}.tar.gz"
    tar -czf "dist/$archive" -C "$package_dir" supermetry LICENSE NOTICE supermetry.service
done

(
    cd dist
    sha256sum supermetry_*.tar.gz >SHA256SUMS
)

echo "Release artifacts written to $repo_root/dist"
