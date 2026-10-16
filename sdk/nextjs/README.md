# `@supermetry/nextjs`

[Supermetry](https://github.com/velixio/supermetry) analytics for the Next.js App Router (Next 14+), with an optional first-party proxy.

```sh
npm install @supermetry/nextjs
```

## Quick start

```tsx
// app/layout.tsx
import { Supermetry } from "@supermetry/nextjs";

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <Supermetry site="your-site-id" />
        {children}
      </body>
    </html>
  );
}
```

App Router navigations are tracked automatically. Track events from client components:

```tsx
"use client";
import { useSupermetry } from "@supermetry/nextjs";

export function SignupButton() {
  const supermetry = useSupermetry();
  return <button onClick={() => supermetry.track("Signup", { plan: "pro" })}>Sign up</button>;
}
```

`<Supermetry />` accepts every [`@supermetry/tracker` option](https://github.com/velixio/supermetry/blob/main/sdk/js/README.md). Functions such as `beforeSend` cannot cross the server/client boundary, so render `<Supermetry beforeSend={…} />` from your own `"use client"` component when you need one.

## First-party proxy

Serve the script and events from your own domain so ad blockers and CSP rules treat them as first-party. Pass `host=""` to `<Supermetry />`; it then loads `/js/script.js` and posts to `/api/public/v1/events` on your origin.

```ts
// lib/supermetry.ts
import { createSupermetryProxy } from "@supermetry/nextjs/proxy";

export const proxy = createSupermetryProxy(); // forwards to Supermetry Cloud; pass { host } when self-hosting
```

```ts
// app/js/[file]/route.ts
import { proxy } from "@/lib/supermetry";
export const { GET, HEAD } = proxy;
```

```ts
// app/api/public/v1/events/route.ts
import { proxy } from "@/lib/supermetry";
export const { POST } = proxy;
```

```tsx
<Supermetry site="your-site-id" />
```

The proxy forwards the visitor's `User-Agent`, `Origin`, `Referer`, `DNT` and `Sec-GPC`, sends the client IP (first `X-Forwarded-For` hop, or `X-Real-IP`) as `X-Forwarded-For`, and answers events with `Cache-Control: no-store`. Add your Next.js server's address to `SUPERMETRY_TRUSTED_PROXIES` so Supermetry trusts the forwarded IP. If your platform exposes the client IP differently, pass `getClientIp: (request) => …`.

## Server-side events

For events from route handlers, server actions or webhooks, use [`@supermetry/node`](https://www.npmjs.com/package/@supermetry/node).
