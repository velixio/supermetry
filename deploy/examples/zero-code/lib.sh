#!/usr/bin/env bash
# Shared by the check scripts: fetches a pinned otelcol-contrib (and jq when
# missing) into a cache outside the repo, verifies sha256, and sets $bin, $jq.
# Needs: curl or wget, tar, sha256sum.
set -euo pipefail

OTELCOL_VERSION=0.163.1
OTELCOL_SHA256=e93e6fd2b59427420a9e8eba5b9e78260df6d21e95283a7298084d2aa07f9df7 # linux_amd64.tar.gz
JQ_VERSION=1.8.1
JQ_SHA256=020468de7539ce70ef1bceaf7cde2e8c4f2ca6c3afb84642aabc5c97d9fc2a0d # jq-linux-amd64

cache=${XDG_CACHE_HOME:-$HOME/.cache}/supermetry-otelcol
mkdir -p "$cache"

fetch() { # url dest
  if command -v curl >/dev/null; then curl -fsSL "$1" -o "$2"; else wget -q "$1" -O "$2"; fi
}

bin=$cache/bin-$OTELCOL_VERSION/otelcol-contrib
if [ ! -x "$bin" ]; then
  tmp=$(mktemp -d "$cache/dl.XXXXXX")
  tgz=otelcol-contrib_${OTELCOL_VERSION}_linux_amd64.tar.gz
  fetch "https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/v$OTELCOL_VERSION/$tgz" "$tmp/$tgz"
  echo "$OTELCOL_SHA256  $tmp/$tgz" | sha256sum -c -
  mkdir -p "$(dirname "$bin")"
  tar xzf "$tmp/$tgz" -C "$(dirname "$bin")" otelcol-contrib
  rm -rf "$tmp"
fi

jq=$(command -v jq || true)
if [ -z "$jq" ]; then
  jq=$cache/jq-$JQ_VERSION
  if [ ! -x "$jq" ]; then
    fetch "https://github.com/jqlang/jq/releases/download/jq-$JQ_VERSION/jq-linux-amd64" "$jq.part"
    echo "$JQ_SHA256  $jq.part" | sha256sum -c -
    chmod +x "$jq.part" && mv "$jq.part" "$jq"
  fi
fi

