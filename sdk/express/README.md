# `@supermetry/express`

Express middleware for [Supermetry](https://github.com/velixio/supermetry/blob/main/sdk/SPEC.md). Attaches `req.supermetry`, bound to the visitor's User-Agent, IP, URL and referrer. Built on [`@supermetry/node`](https://www.npmjs.com/package/@supermetry/node).

## Install

```sh
npm install @supermetry/express
```

## Quick start

```ts
import express from "express";
import { supermetry } from "@supermetry/express";

const app = express();
app.set("trust proxy", true); // so req.ip / req.protocol reflect the real visitor behind your proxy

app.use(
  supermetry({
    site: "your-site-id",
    host: "https://analytics.example.com",
    trackPageviews: true, // pageview for every GET answered 2xx with text/html
    getProfileId: (req) => req.user?.id,
    onError: (error) => console.error(error),
  }),
);

app.post("/api/signup", (req, res) => {
  req.supermetry.track("Signup", { props: { plan: req.body.plan } });
  req.supermetry.identify(newUser.id, { plan: req.body.plan });
  res.json({ ok: true });
});
```

`req.supermetry` has `track(name, options)`, `pageview(options)` and `identify(profileId, traits, context)`, the same as `@supermetry/node`, with the request context merged in (per-call options win). It also exposes `context` and the underlying `client`.

**Profile IDs are per request.** The underlying client is shared across all visitors, so it never stores a profile ID. `req.supermetry.identify(id)` attaches `id` to the later `track`/`pageview` calls of *that request only*. For every other request, use `getProfileId` (e.g. from your session) or pass `profileId` per call.

## Options

All `@supermetry/node` options (`site`, `host`, `url`, `userAgent`, `disabled`, `onError`, `fetch`, …), plus:

| option | |
|---|---|
| `client` | Reuse an existing `Supermetry` instance instead of `site`/`host` |
| `trackPageviews` | `true`: pageview for GET requests answered 2xx with `text/html`. Function `(req, res) => boolean`: called when each response finishes |
| `getUrl` | `(req) => string`; default `req.protocol + "://" + req.get("host") + req.originalUrl`. Use it when the public host differs from the `Host` header |
| `getProfileId` | `(req) => string \| undefined`; attached as `profile_id` to every event of that request |

Wait for in-flight events on shutdown with `await client.flush()` (create the client yourself and pass it as `client`).

## Server setup

- Add your app servers to `SUPERMETRY_TRUSTED_PROXIES` on the Supermetry server so the forwarded visitor IP is used.
- Turn off *strict origin* for the site, or create the client with `sendOrigin: true`.
- The request URL's host must be an allowed host of the site.

TypeScript: importing the package augments `Express.Request` with `supermetry`.

License: Apache-2.0
