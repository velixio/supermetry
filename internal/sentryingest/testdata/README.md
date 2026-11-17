# Sentry envelope fixtures

Raw envelope requests recorded from real Sentry SDKs against the capture
server in `tools/sentry-fixtures/capture` (recorded 2026-11-17). Each case is
`<case>.envelope` (the request body exactly as received, still compressed when
the SDK compressed it) plus `<case>.json` (method, path, query, a short header
allow-list, `sdk`, `sdk_version`, `notes`).

| Dir | SDK | Version | Cases |
|---|---|---|---|
| `node/` | `@sentry/node` | 11.13.0 | unhandled-exception, unhandled-rejection, captured-message, session-aggregates, client-report |
| `browser/` | `@sentry/browser` + headless Chromium via `playwright-core` 1.63.0 | 11.13.0 | onerror, unhandledrejection, breadcrumbs, session, client-report |
| `electron/` | `@sentry/electron` on Electron 40.10.6 under `xvfb-run` | 8.4.0 | main-exception, renderer-exception, native-crash (gzip body, with an `event.minidump` attachment) |
| `python/` | `sentry-sdk` | 2.75.0 | unhandled-exception, logging-error, wsgi-sessions, client-report (gzip); unhandled-exception-br (`Content-Encoding: br`) |
| `go/` | `sentry-go` (separate module) | 0.50.0 | captured-exception, recovered-panic |
| `android/`, `cocoa/` | hand-built, **synthetic** (`"synthetic": true` in the sidecar) | n/a | see each sidecar's `notes` for the SDK source reference |

Notes on how they differ from a plain `init`:

- Node: `rewriteFramesIntegration` is added so stack frames carry `app:///` paths instead of this machine's. Other integrations are the defaults. `serverName` is `fixture-host`.
- Python: run from `/tmp/fixture-app`, `include_local_variables=False`, `server_name=fixture-host`; the default gzip body is forced (the venv has `brotli`, which the SDK would otherwise pick); the `-br` case sets `transport_compression_algo=br`.
- Go: built with `-trimpath`; `TZ=UTC`.
- Electron `native-crash`: the minidump's stack memory held this machine's environment, so printable runs containing paths, the user name, hostname or emails were overwritten with `x` of the same length by `tools/sentry-fixtures/scrub-binary.py`, and the body was re-gzipped. Everything else is as sent.
- Cases that are not unhandled-error shaped use the SDK's own mechanism (`beforeSend` returning null for client reports, an HTTP request for session aggregates).

Re-recording: each SDK directory under `tools/sentry-fixtures/` has its lockfile and generator.
Install (Bun: `bun install --frozen-lockfile`; Python: `uv venv .venv && uv pip install --require-hashes -r requirements.txt`),
then run `tools/sentry-fixtures/record-case.sh <sdk> <case> <first item type> <notes> -- <generator command>`
for each case (see the commands in the sidecars' `notes`), and finish with
`tools/sentry-fixtures/scrubcheck.sh internal/sentryingest/testdata`. `ELECTRON_RUN_AS_NODE` must be unset for Electron.
Delete `node_modules`, `.venv` and `dist` afterwards (they are git-ignored).
