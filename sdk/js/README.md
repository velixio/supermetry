# `@supermetry/tracker`

Typed browser wrapper for Supermetry's tracking script. It injects the script; it does not bundle the tracker itself.

```sh
npm install @supermetry/tracker
```

## Quick start

```ts
import { init } from "@supermetry/tracker";

const supermetry = init({
  site: "your-site-id",
  beforeSend: (event) => ({
    ...event,
    props: { ...event.props, app: "storefront" },
  }),
});

supermetry.track("Signup", { plan: "pro", seats: 3, trial: true });
supermetry.track("Purchase", { sku: "tee" }, { revenue: { amount: "49.90", currency: "USD" } });
supermetry.identify("user_123", { plan: "pro" });
supermetry.identify(null);
```

`site` is required. Events go to Supermetry Cloud (`https://app.supermetry.com`) unless you set `host` or `src`:

- `host` is your Supermetry origin when self-hosting. It derives `src` (`{host}/js/script.js`, or `script.full.js` when `manual`, `consent` or `beforeSend` is set) and `api` (`{host}/api/public/v1/events`). Use `host: ""` for a first-party proxy on your own origin.
- `src` and `api` set the script URL and events endpoint explicitly.

The optional `manual`, `hash`, `consent`, `forms`, `outbound`, `downloads`, `profiles`, and `allowLocalhost` settings configure the corresponding tracker data attributes.

## API

| Method | |
|---|---|
| `pageview({ url?, props? })` | Records a pageview. Needed only in `manual` mode. |
| `track(name, props?, { revenue? })` | Custom event. Prop values are strings, numbers or booleans; revenue is `{ amount: string \| number, currency? }`. |
| `identify(id, traits?)` | Attaches a profile ID (≤128 chars) to later events and sends string traits. `identify(null)` clears it. Identification is memory-only; call it on every page load. |
| `consent(granted)` | Resolves `consent: true`. Events wait until the visitor chooses. |
| `optOut()` / `optIn()` | Persists the visitor's opt-out flag in `localStorage`. |

Calls made before the script loads, or before `init`, are queued in memory. `getTracker()` returns the same tracker from anywhere. Repeated `init` calls in the same page return the original tracker. During server rendering, every method is a no-op and nothing touches the DOM.

## First-party proxy

`@supermetry/tracker/proxy` exports `createProxyHandler({ host })`, a Fetch API `(Request) => Promise<Response>` handler that serves `/js/*.js` and `/replay.js`, and forwards `POST /api/public/v1/events` and session replay uploads (`POST /api/replay/start`, `POST /api/replay/chunk`, `PUT /api/replay/css/{hash}`), with the visitor's `User-Agent` and client IP in `X-Forwarded-For`. Framework packages wrap it (`@supermetry/nextjs`, `@supermetry/remix`). Mount it under any path prefix (for example `/stats/`): the tracker loads replay from the same prefix as `data-api`.

On Supermetry Cloud, pass the site's first-party proxy key (`proxyKey: process.env.SUPERMETRY_PROXY_KEY`, created on the site's installation page). Tracking requests then carry `X-Supermetry-Proxy-Key` and `X-Supermetry-Client-IP`, so Cloud counts real visitors instead of your server's address. The key is a server-side secret: never put it in client code. Self-hosted instances use `SUPERMETRY_TRUSTED_PROXIES` instead.
