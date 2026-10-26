# Supermetry SDK contract

Every SDK in `sdk/` sends events to one endpoint and must follow this contract exactly.
The server source of truth is `internal/ingest/payload.go` and `internal/ingest/handler.go`.

## Endpoint

`POST {host}/api/public/v1/events` with a JSON body (`Content-Type: application/json`, or
`text/plain` from `sendBeacon`). No API key. Answers `202 Accepted` on success (also for
events silently dropped by privacy, bot, exclusion or dedup rules), `400` for an invalid
payload or a URL whose host is not in the site's allowed hosts, `429` with `Retry-After`
when rate limited.

The browser tracker script is served at `{host}/js/script.js` (`script.full.js` is the
full build with manual mode, consent, `beforeSend`, forms, outbound, downloads, vitals).

## Payload

| field | type | notes |
|---|---|---|
| `v` | int | always `1` |
| `id` | string | 1–64 chars of `[A-Za-z0-9_-]`; dedup key, keep it stable across retries (UUID without dashes) |
| `ts` | int | Unix milliseconds. Older than 24 h is dropped; more than 5 min ahead is clamped |
| `site` | string | **required**, public site ID |
| `name` | string | **required**, ≤300 bytes. `pageview` for page/screen views, `identify` for identify, anything else is a custom event |
| `url` | string | **required**, absolute `http(s)` URL, ≤2000 bytes, no userinfo. Its host must be an allowed host of the site |
| `referrer` | string | optional, ≤2000 bytes |
| `screen_width` | int | 0–32767, optional |
| `props` | object | ≤30 keys; values string, number or boolean (server stringifies). Key ≤300 bytes, value ≤2000 bytes |
| `revenue` | object | `{ "amount": "49.90" \| 49.9, "currency": "USD" }`; ignored on `pageview`; max 6 decimals; currency is ISO 4217 and must match the site currency (or be omitted) |
| `profile_id` | string | 1–128 chars. After `identify`, attach to every event |
| `traits` | object | string → string, ≤20 keys, key ≤64 chars, value ≤256 chars. **Only allowed on `name: "identify"`**; on any other event the whole request is rejected |

Body max 8 KB. Unknown fields are ignored; client-supplied visitor or session IDs are never
used. Visitors are derived server-side from IP + User-Agent + a daily salt.

## Request headers that matter

- `User-Agent`: parsed for browser/OS/device and bot filtering. Generic HTTP-client UAs
  (`node-fetch`, `axios`, `okhttp`, `Go-http-client`, `python-requests`, `curl`) are on the
  crawler list and are **dropped as bots**. SDKs must set their own UA
  (`supermetry-node/0.1.0`, `Supermetry-Ruby/0.1.0`…) and server SDKs must let callers
  forward the visitor's real UA. Mobile SDKs should send a browser-shaped UA so OS/device
  parse, e.g. `Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Supermetry-iOS/0.1.0`
  and `Mozilla/5.0 (Linux; Android 14; Pixel 8) Supermetry-Android/0.1.0`.
- `X-Forwarded-For`: server SDKs forward the visitor IP here (trusted only from
  `SUPERMETRY_TRUSTED_PROXIES`).
- `DNT: 1` / `Sec-GPC: 1` cause a drop on sites that respect privacy signals.
- Sites with *strict origin* require `Origin`/`Referer`; server SDKs document that strict
  origin should be off for server-sent events, and may send `Origin` derived from the event URL.

## Shared SDK surface (adapt names to the language's idiom)

- Config: `site` (required), `host` (Supermetry origin: `https://app.supermetry.com` for Cloud, which the npm packages
  use by default, or the self-hosted URL;
  endpoint = host + `/api/public/v1/events`, overridable with `api`/`endpoint`), `disabled`
  flag, `onError` / logger hook.
- `track(name, props?, { revenue?, profileId?, url?, referrer? })`
- `pageview(url? / screen?, props?)`
- `identify(profileId, traits?)` → sends `name: "identify"` with `profile_id` + `traits`,
  stores the profile ID on the client so later events carry `profile_id`. `identify(null)` /
  `reset()` clears it. Server SDKs are the exception: a server client is shared across
  visitors, so it never stores the profile ID and callers pass `profileId` per event.
- Server SDKs: per-call `userAgent` and `ip` (forwarded as `User-Agent` / `X-Forwarded-For`),
  a required `url` per event (or a configured default `url`), fire-and-forget by default with
  an awaitable / `flush()` path. Never throw from tracking into the host app unless the caller
  opts in.
- Mobile SDKs: `appUrl` config (e.g. `https://app.supermetry.com`, must be an allowed host);
  `screen("Checkout")` sends `pageview` with `url = appUrl + "/" + screen path`. Persist
  `profile_id` across launches; queue events in memory (and on disk where cheap), retry with
  the same `id`/`ts` on network failure or 429/5xx, drop on 400. Flush when the app
  backgrounds. `optOut()`/`optIn()` persisted.
- Validate locally where cheap (drop invalid traits rather than letting the server reject
  the whole event) and truncate props to 30 keys.
- Version `0.1.0`, license Apache-2.0, each package has a README with install + quick start.
