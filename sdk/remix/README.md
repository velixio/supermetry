# `@supermetry/remix`

[Supermetry](https://github.com/supermetry) analytics for Remix 2 (and React Router 7 framework mode): route-aware pageviews plus a first-party proxy.

```sh
npm install @supermetry/remix
```

## Quick start

```tsx
// app/root.tsx
import { Links, Meta, Outlet, Scripts } from "@remix-run/react";
import { Supermetry } from "@supermetry/remix";

export default function App() {
  return (
    <html lang="en">
      <head>
        <Meta />
        <Links />
      </head>
      <body>
        <Outlet />
        <Supermetry site="your-site-id" host="https://analytics.example.com" />
        <Scripts />
      </body>
    </html>
  );
}
```

`<Supermetry />` loads the tracker in manual mode and sends a pageview for the first route and every change of `pathname` or `search` (and `hash` with `hash`). It accepts every [`@supermetry/tracker` option](../js/README.md) except `manual`.

```tsx
import { useSupermetry } from "@supermetry/remix";

const supermetry = useSupermetry();
supermetry.track("Signup", { plan: "pro" });
supermetry.identify(user.id, { plan: user.plan });
```

### React Router 7

`@supermetry/remix` imports `useLocation` from `@remix-run/react`. With React Router 7, use the router-agnostic entry and pass the location yourself:

```tsx
import { useLocation } from "react-router";
import { useSupermetryPageviews } from "@supermetry/remix/router";

function Analytics() {
  useSupermetryPageviews(useLocation(), { site: "your-site-id", host: "https://analytics.example.com" });
  return null;
}
```

## First-party proxy

Omit `host` and add two resource routes; the script then loads from `/js/script.full.js` and events post to `/api/public/v1/events` on your origin.

```ts
// app/supermetry.server.ts
import { createSupermetryProxy } from "@supermetry/remix/proxy";

export const proxy = createSupermetryProxy({ host: "https://analytics.example.com" });
```

```ts
// app/routes/js.$file.ts
import { proxy } from "~/supermetry.server";
export const loader = proxy.loader;
```

```ts
// app/routes/api.public.v1.events.ts
import { proxy } from "~/supermetry.server";
export const action = proxy.action;
```

The proxy forwards the visitor's `User-Agent`, `Origin`, `Referer`, `DNT` and `Sec-GPC`, sends the client IP (first `X-Forwarded-For` hop, or `X-Real-IP`) as `X-Forwarded-For`, and answers events with `Cache-Control: no-store`. Add your server's address to `SUPERMETRY_TRUSTED_PROXIES`. Pass `getClientIp: (request) => …` if your adapter exposes the IP elsewhere.

For server-side events use [`@supermetry/node`](../node/README.md).
