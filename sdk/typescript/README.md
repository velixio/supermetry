# @supermetry/app-health (TypeScript)

Crash and error reports from your app to [Supermetry App Health](../../docs/guide/apps.md). No runtime dependencies. Uses `fetch`; works in Node 18+, Electron (main and renderer), Deno and Bun. MIT licensed, small enough to vendor.

## Use

```ts
import { init, startSession, captureException, captureCrash, flush } from "@supermetry/app-health";

init({
  appId: "app_xxxxxxxx",          // the app's public id
  ingestKey: "smk_xxxxxxxx",   // the app's ingest key
  host: "https://app.supermetry.example",
  release: "1.4.2",
  build: "a1b2c3d",                // optional
  environment: "production",       // optional
  installId: myRandomInstallId,    // optional, supplied by you
  enabled: userConsented,          // default true
});
startSession();                    // once per run: powers crash-free sessions

try { risky(); } catch (err) { captureException(err, { handled: true, tags: { op: "export" } }); }

process.on("uncaughtException", async (err) => {
  captureException(err, { handled: false, level: "fatal" });
  await flush(2000);
  process.exit(1);
});
```

`captureCrash({ errorType, exitReason, signal, exitCode, frames, tags })` sends `kind: "crash"`, level `fatal`. `captureEvent(kind, fields)` sends `install_failed`, `incompatibility`, `operation_failed`, `hang` and the other kinds. `track(name, props?)` records a custom product event and `screen(name, props?)` a screen view, for the app's product analytics (active users, funnels, retention). Both need `installId`, queue and batch like every other report, never send a user id, and ignore a name the server would reject: event names are 1 to 64 characters of letters, digits and `_ . : $ - ` and space, and must not start with `$`; props are strings (up to 20 keys, values up to 200 bytes, no `$` keys). `setEnabled(bool)`, `flush(timeoutMs?)` and `shutdown()` finish the API. `createClient(options)` gives an independent instance.

Options: `maxQueue` (100), `flushIntervalMs` (5000, `0` disables the timer), `maxAgeDays` (7, older events are discarded unsent), `inApp(path)` (first-party filter; the default drops `node_modules` and `node:` internals), `onDrop(events, reason)`, `osVersion`, `deviceClass`, `transport`, `storage`.

## Delivery

Events queue in memory (oldest dropped first when full) and go out in batches of at most 20 events and 16 KB, with header `X-Supermetry-Key` and body `{"site": appId, "events": [...]}`. Each event has a random 32-hex `id`, reused on retry so the server can drop duplicates. Network errors, 429 and 5xx retry with exponential backoff (1 s, doubling, capped at 5 min, with jitter). 400 and 401 drop the batch and call `onDrop`. The User-Agent is `supermetry-js/<version>` where the runtime lets you set it.

## Surviving restarts (optional)

Pass a `storage` adapter with `load()` and `save(events)`. Electron main-process example, using a JSON file in the user data folder:

```ts
import { app } from "electron";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const file = join(app.getPath("userData"), "supermetry-queue.json");
init({
  /* ...options */
  storage: {
    load: () => { try { return JSON.parse(readFileSync(file, "utf8")); } catch { return []; } },
    save: (events) => { try { writeFileSync(file, JSON.stringify(events)); } catch {} },
  },
});
```

## Privacy

- **The exception message is never sent.** Only the class name (`error_type`) and the stack frames leave the app. Frames are `path:line function` lines: first-party only, home folders replaced with `~/`, other absolute paths cut to their last 3 segments, at most 4 KB.
- No user names, file contents or environment variables are collected. Tag values are what you pass in, so keep personal data out of them.
- `installId` is **supplied by your app**. If you don't pass one, the kit omits it and does not generate one. Use a random per-install id, not an account id or an email address.
- The kit sends nothing until it is enabled. Pass `enabled: false` at startup and call `setEnabled(true)` once the user agrees; `setEnabled(false)` clears the queue.

## Other platforms

Swift and Kotlin kits are not published yet. The wire format is plain JSON over HTTPS; see [docs/guide/apps.md](../../docs/guide/apps.md) and implement the same queue rules.

## Development

```sh
bun install && bun test && bunx tsc --noEmit && bun run build   # build writes dist/ (git-ignored)
```
