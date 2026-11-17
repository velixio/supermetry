# Sentry fixture capture

Dev-only tooling that records the raw envelope requests real Sentry SDKs send,
so the ingest tests have golden fixtures. Nothing here ships in the binary and
no Sentry SDK is ever added to the main `go.mod`.

- `capture/`: stdlib HTTP server. `go run ./tools/sentry-fixtures/capture -addr 127.0.0.1:8977 -out <dir> -sdk <name>`.
  Writes `<seq>-<first item type>.envelope` (raw body as received, still
  compressed if the SDK compressed it) and `<seq>-<type>.json` (method, path,
  query, headers `Content-Type`, `Content-Encoding`, `X-Sentry-Auth`,
  `User-Agent`, `sdk`, `sdk_version`, `notes`).
- `scrubcheck.sh <dir>...`: fails on machine paths, hostname, username, IPv4
  outside `127.0.0.1` / `192.0.2.0/24`, and emails not at `example.com`.
- Per-SDK generators live in sibling directories with their own lockfiles.

DSN used by every generator: `http://0123456789abcdef0123456789abcdef@127.0.0.1:<port>/42`.
Generated data must be synthetic: user id `fixture-user-1`, emails
`@example.com`, IPs from `192.0.2.0/24`.
