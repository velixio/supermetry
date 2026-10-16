# `@supermetry/node`

Server-side SDK for [Supermetry](https://github.com/velixio/supermetry) self-hosted analytics. Zero runtime dependencies; uses the global `fetch` (Node.js 18+). See the [event contract](https://github.com/velixio/supermetry/blob/main/sdk/SPEC.md) for payload details.

## Install

```sh
npm install @supermetry/node
```

## Quick start

```ts
import { Supermetry } from "@supermetry/node";

const supermetry = new Supermetry({
  site: "your-site-id",
  url: "https://shop.example.com/", // optional default page URL
  onError: (error) => console.error(error),
});

// Fire-and-forget: never throws, never rejects.
supermetry.track("Signup", {
  url: "https://shop.example.com/signup",
  props: { plan: "pro", seats: 3 },
  revenue: { amount: "49.90", currency: "USD" },
  profileId: "user_42",
  userAgent: req.headers["user-agent"], // forward the visitor's UA
  ip: req.ip,                           // forwarded as X-Forwarded-For
});

supermetry.pageview({ url: "/pricing", userAgent, ip }); // relative URLs resolve against `url`
supermetry.identify("user_42", { email: "ada@example.com", plan: "pro" }, { userAgent, ip });

// Before the process exits (serverless handlers, scripts, graceful shutdown):
await supermetry.flush(); // or: await supermetry.close();
```

Every call returns a `Promise<SendResult>` (`{ ok, status?, error? }`) that never rejects, so you can `await` it when you need to know the outcome.

## Options

| option | default | |
|---|---|---|
| `site` | required | Public site ID |
| `host` | `https://app.supermetry.com` | Supermetry origin; set it when self-hosting. Events go to `host + /api/public/v1/events` |
| `endpoint` | | Full endpoint URL override |
| `url` | | Default event URL; base for relative event URLs |
| `userAgent` | `supermetry-node/0.2.0` | UA when the call does not forward a visitor UA |
| `disabled` | `false` | Send nothing |
| `onError` | no-op | Receives a `SupermetryError` (validation, network, HTTP status) |
| `fetch` | `globalThis.fetch` | Custom fetch |
| `timeout` | `10000` | Per-attempt timeout (ms) |
| `retry` | `true` | Retry once on network error / 429 / 5xx with the same event `id` |
| `sendOrigin` | `false` | Send `Origin` derived from the event URL |

Per-call context (`track`, `pageview`, third argument of `identify`): `url`, `referrer`, `profileId`, `userAgent`, `ip`, `screenWidth`, `timestamp`.

## Notes

- **User-Agent matters.** Generic HTTP-client UAs (`node-fetch`, `axios`, `undici`…) are filtered as bots. Forward the visitor's UA when you have it; otherwise the SDK sends `supermetry-node/0.2.0`.
- **Visitor IP** is sent as `X-Forwarded-For`. The Supermetry server only trusts it from addresses listed in `SUPERMETRY_TRUSTED_PROXIES`; add your app servers there, or every event counts as one visitor.
- **Strict origin**: turn it off for sites that receive server-side events (or set `sendOrigin: true`).
- **`identify` does not store the profile ID.** A server client is shared across visitors, so a stored ID would leak between users. Pass `profileId` on every call for identified users. `identify` sends the traits (string values, max 20; invalid traits are dropped locally since the server rejects the whole event otherwise).
- Props are limited to 30 keys of string/number/boolean values; others are dropped.
- Event `url` hosts must be allowed hosts of the site, or the server answers `400`.

License: Apache-2.0
