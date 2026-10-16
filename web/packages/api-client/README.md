# `@supermetry/api-client`

Typed TypeScript client for Supermetry's versioned public API. The request and response types are generated from the OpenAPI contract shipped with Supermetry, while `openapi-fetch` keeps the runtime small.

## Install

```sh
bun add @supermetry/api-client
```

## Create a client

Create an API key in **Organization settings → API keys**. Give read integrations the `stats:read` scope and keep the secret on your server.

```ts
import { createSupermetryClient } from '@supermetry/api-client'

const supermetry = createSupermetryClient({
  apiKey: process.env.SUPERMETRY_API_KEY!,
})

const { data, error, response } = await supermetry.GET('/api/public/v1/stats/summary', {
  params: {
    query: {
      site_id: 42,
      period: '30d',
    },
  },
})

if (!response.ok) throw new Error(error.detail)
console.log(data.totals)
```

Requests go to Supermetry Cloud (`https://app.supermetry.com`) by default. When self-hosting, pass `baseUrl` with your own Supermetry URL.

The client adds `Authorization: Bearer <key>` to every request. Rate-limit metadata is returned in the `RateLimit-Limit`, `RateLimit-Remaining`, and `RateLimit-Reset` response headers. A `429` response can also include `Retry-After`.

Collection endpoints use cursor pagination. Pass the response's `next_cursor` back as `cursor`; a null cursor means there are no more pages. API errors use RFC 9457 `application/problem+json` responses.

For CSV exports, call a stats or query endpoint with `format=csv`, or send `Accept: text/csv`. Supported exports accept `limit=all` and stream at most 100,000 rows.

The complete endpoint and schema reference is available inside Supermetry under **Organization settings → API reference**. MCP clients can connect to `/api/public/v1/mcp` with the same bearer key.

## Regenerate and build

```sh
bun run gen
bun run build
```
