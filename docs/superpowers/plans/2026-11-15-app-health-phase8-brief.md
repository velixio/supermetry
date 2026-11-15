# App Health Phase 8 brief: client kits (TypeScript, Python)

Spec: `docs/superpowers/specs/2026-11-15-app-health-design.md` (§2 wire format, §9 client kits, Privacy).
Wire contract: `docs/superpowers/plans/2026-11-15-app-health-phase3-brief.md`, section "Endpoint". It is binding: body shape, header, limits, reply, drop reasons. The server isn't on this branch, so test against an in-process fake server that enforces that contract.

Rulings:
- **Location**: `sdk/typescript/` and `sdk/python/` in this repo, each with an MIT `LICENSE` (copyright "Supermetry contributors") and a README. Also add one line to the root README's docs list.
- **Swift and Kotlin**: not built (no toolchain here). Each README has a "Other platforms" note pointing to the wire format in `docs/guide/apps.md`.
- **No runtime dependencies.** TypeScript uses `fetch` and works in Node ≥18, Electron main, Electron renderer, Deno and Bun. Python uses only the stdlib (`urllib`, `threading`, `json`, `hashlib`), Python ≥3.9.
- **Shared API.** The two kits have the same shape (TS camelCase, Python snake_case):
  - `init({ appId, ingestKey, host, release, build?, environment?, installId?, enabled? = true, maxQueue? = 100, flushIntervalMs? = 5000, maxAgeDays? = 7, transport? })`
  - `captureException(err, { handled?, level?, tags? })`: builds `kind: "error"`, takes `error_type` from the class name, and builds frames from the stack. It **never sends the message**.
  - `captureCrash({ errorType?, exitReason?, signal?, exitCode?, frames?, tags? })` → `kind: "crash"`, `level: "fatal"`.
  - `captureEvent(kind, fields)` for `install_failed`, `incompatibility`, `operation_failed`, `hang`.
  - `startSession()` sends `kind: "session"` once per process with a random `session_id`.
  - `setEnabled(bool)`: consent switch. Turning it off clears the queue and stops sending.
  - `flush(): Promise<void>` / `flush(timeout)`.
  - `shutdown()`.
- **Event fields**:
  - `platform`, `arch`, `os_version` (major.minor) and `runtime` are filled automatically.
  - Each event gets a random `id` of 32 hex chars, used for dedup on retry.
  - `ts` is the time of capture.
- **Queue**:
  - In memory, bounded (oldest dropped first).
  - Batches of ≤20 events and ≤16 KB.
  - Retry with exponential backoff (1 s, doubling, capped at 5 min, with jitter) on network errors, 429 and 5xx.
  - No retry on 400 or 401: drop the batch and call an optional `onDrop` hook.
  - Events older than `maxAgeDays` are discarded before sending.
  - Persistence across restarts is optional: a `storage` adapter interface (`load()/save(events)`), off by default. Python ships a JSON-file adapter; TS documents an Electron example in the README.
- **Frames**:
  - Parse V8 stacks (TS) and `traceback` frames (Python) into `path:line function` lines.
  - Keep first-party frames (an `inApp(path)` option; by default, drop `node_modules`, `site-packages` and `<frozen…>`).
  - Scrub them on the client with the same rules as the server's `ScrubFrames` (home folder → `~/`, other absolute paths → last 3 segments), and cap them at 4 KB.
- **Privacy**:
  - Never send exception messages, user names, file contents or environment variables.
  - `installId` is supplied by the app. If it's absent, the kit omits it; it doesn't generate one.
  - Document both in each README.
- **User-Agent**: `supermetry-js/<version>` / `supermetry-python/<version>` where the runtime allows setting it. Browsers forbid it, which is fine because the app intake has no bot filter.

Tests (test-first):
- Batching splits at 20 events and at 16 KB.
- Retries back off on 429 and 5xx, use the same ids, and stop on 401.
- `setEnabled(false)` clears the queue.
- Too-old events are discarded.
- Frames are parsed and scrubbed (golden fixtures for V8 and Python tracebacks).
- `captureException` never includes the message anywhere in the request body.
- `flush` resolves when the queue is empty.
- Requests carry the `X-Supermetry-Key` header, with the body shaped `{"site":appId,"events":[…]}`.
- TS: `bun test` or vitest in `sdk/typescript`, run with Bun, plus a `tsc --noEmit` typecheck and an ESM build to `dist/` (git-ignored).
- Python: `uv run --with pytest pytest` in `sdk/python`, with `pyproject.toml` (hatchling) and package `supermetry_apps`.

Process:
- Write a short plan (≤120 lines) at `docs/superpowers/plans/2026-11-15-app-health-phase8-sdks.md`, commit it, and execute it test-first.
- Make 4–6 commits.
- Don't add these packages to the root web workspace or to Go modules.
