#!/usr/bin/env bash
# Runs app.py from a neutral path so frames do not carry this machine's paths.
set -e
here="$(cd "$(dirname "$0")" && pwd)"
mkdir -p /tmp/fixture-app
cp "$here/app.py" /tmp/fixture-app/app.py
cd /tmp/fixture-app
exec "$here/.venv/bin/python" app.py "$@"
