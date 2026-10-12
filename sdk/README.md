# Supermetry SDKs

Lightweight SDKs for sending events to a Supermetry server. They all speak the same
event contract, described in [SPEC.md](SPEC.md).

| Platform | Package | Directory |
|---|---|---|
| HTML / Script | none, use the script tag below | |
| Browser (typed wrapper) | `@supermetry/tracker` | [js](js) |
| React | `@supermetry/react` | [react](react) |
| Next.js | `@supermetry/nextjs` | [nextjs](nextjs) |
| Remix | `@supermetry/remix` | [remix](remix) |
| Vue | `@supermetry/vue` | [vue](vue) |
| Astro | `@supermetry/astro` | [astro](astro) |
| REST API | none, see below | |
| React Native | `@supermetry/react-native` | [react-native](react-native) |
| Node | `@supermetry/node` | [node](node) |
| Express | `@supermetry/express` | [express](express) |
| Laravel | `supermetry/laravel` (Composer) | [laravel](laravel) |
| Rust | `supermetry` (crates.io) | [rust](rust) |
| Ruby | `supermetry` (RubyGems) | [ruby](ruby) |
| iOS (Swift) | `Supermetry` (Swift Package) | [ios](ios) |
| Android (Kotlin) | `com.supermetry:supermetry-android` | [android](android) |

## HTML / Script

```html
<script defer data-site="YOUR_SITE_ID" src="https://analytics.example.com/js/script.js"></script>
```

Then, anywhere on the page:

```js
supermetry.track("Signup", { plan: "pro" });
supermetry.track("Purchase", {}, { revenue: { amount: "49.90", currency: "USD" } });
supermetry.identify("customer-42", { plan: "pro" });
```

Options are `data-` attributes on the tag: `data-api`, `data-manual`, `data-hash`,
`data-consent="required"`, `data-forms`, `data-outbound`, `data-downloads`,
`data-allow-localhost`. Manual mode, consent and `beforeSend` load the full build
(`script.full.js`) automatically.

## REST API

```sh
curl -X POST https://analytics.example.com/api/public/v1/events \
  -H "Content-Type: application/json" \
  -H "User-Agent: $VISITOR_USER_AGENT" \
  -H "X-Forwarded-For: $VISITOR_IP" \
  -d '{"site":"YOUR_SITE_ID","name":"Signup","url":"https://example.com/signup","props":{"plan":"pro"}}'
```

No API key is needed. The server answers `202 Accepted`. `url` must be on one of the site's
allowed hosts. Forward the visitor's User-Agent and IP, because generic HTTP-client user
agents such as `curl` or `node-fetch` are filtered as bots. Turn off **strict origin** for
sites that receive server-sent events. See [SPEC.md](SPEC.md) for every field and limit, and
[docs/api.md](../docs/api.md) for the read API.

## Developing

The JavaScript packages are a Bun workspace:

```sh
cd sdk && bun install
bun run build       # dependency order: tracker and node first
bun run typecheck
bun run test
```
