# App Health Phase 8 plan: client kits

Brief: `2026-11-15-app-health-phase8-brief.md` (binding). Wire contract: phase 3 brief, "Endpoint".

## Layout
- `sdk/typescript/` (`@supermetry/app-health`): `src/{index,client,queue,frames,platform,types}.ts`, `test/*.test.ts`, `test/fake-server.ts`, `LICENSE`, `README.md`, `tsconfig.json`, `package.json`. Zero runtime deps; dev: typescript, bun types.
- `sdk/python/` (`supermetry-apps`, package `supermetry_apps`): `client.py`, `queue.py`, `frames.py`, `storage.py`, `tests/`, `pyproject.toml` (hatchling), `LICENSE`, `README.md`. Stdlib only, Python >= 3.9.
- Root README: one line in the docs list.

## Design
- Queue: in-memory deque bounded by `maxQueue` (oldest dropped); batches <= 20 events and <= 16 KB body; backoff 1 s doubling, cap 5 min, jitter; retry on network error/429/5xx with the same event ids; 400/401 drop the batch and call `onDrop`; events older than `maxAgeDays` discarded before send; optional `storage` adapter (`load()/save(events)`).
- Frames: parse V8 stack / Python traceback into `path:line function`, drop `node_modules`, `site-packages`, `<frozen...>` (override with `inApp`), scrub like server `ScrubFrames` (home -> `~/`, other absolute -> last 3 segments, strip `file://`), cap 4 KB on a line boundary.
- Privacy: exception message never read into the payload; installId only if supplied.
- Transport injectable for tests; default is `fetch` / `urllib`.

## Tasks (test-first, RED then GREEN)
1. Plan (this file).
2. TS: frames tests (V8 golden, scrub, cap) -> frames.ts.
3. TS: client/queue tests against in-process fake server (batching 20/16 KB, retry+same ids, 401 stop, setEnabled clears, too old, no message, flush, header/body shape) -> client.ts; tsc + ESM build.
4. Python: same tests (frames golden traceback, queue, client) -> implementation, JSON-file storage adapter.
5. READMEs, LICENSEs, root README line.

## Verification
`cd sdk/typescript && bun install && bun test && bunx tsc --noEmit && bun run build`
`cd sdk/python && uv run --with pytest pytest`
