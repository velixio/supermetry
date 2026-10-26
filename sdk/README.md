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
<script defer data-site="YOUR_SITE_ID" src="https://app.supermetry.com/js/script.js"></script>
```

Self-hosting? Load the script from your own Supermetry URL instead. The npm packages default to Supermetry Cloud (`https://app.supermetry.com`); pass `host` to point them at your instance.

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
curl -X POST https://app.supermetry.com/api/public/v1/events \
  -H "Content-Type: application/json" \
  -H "User-Agent: $VISITOR_USER_AGENT" \
  -H "X-Forwarded-For: $VISITOR_IP" \
  -d '{"site":"YOUR_SITE_ID","name":"Signup","url":"https://supermetry.com/signup","props":{"plan":"pro"}}'
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

CI runs those checks for every pull request and every push to `main`. It also packs
every npm SDK, installs the tarballs with npm in a clean project, checks their
metadata and entry points, and runs the deeper ESM, CommonJS and TypeScript smoke
test for `@supermetry/node`.

### Publishing npm packages

1. Set the package version and all internal dependency versions, then merge the
   verified change to `main`.
2. Push its matching tag. The accepted prefixes are `sdk-tracker-v`, `sdk-node-v`,
   `sdk-react-native-v`, `sdk-react-v`, `sdk-vue-v`, `sdk-astro-v`, `sdk-express-v`,
   `sdk-nextjs-v`, and `sdk-remix-v`.
3. The `publish-node-sdk.yml` workflow rebuilds and verifies the package, checks the
   tag against `package.json`, and stages that exact artifact on npm with provenance.
4. A maintainer reviews the staged package on npm and approves it with 2FA.

Stage releases in dependency order: tracker, Node and React Native first; React,
Vue, Astro and Express second; Next.js and Remix last. A dependency must be public
before a dependent package is staged. Stable versions use npm's `latest` tag;
prerelease versions use `next`.

For a new package, npm requires a one-time `npm stage publish` from a local,
interactive npm session because trusted publishing can only be configured after
the package exists. Once that first version is staged, configure trusted publishing
for the package using this repository, `publish-node-sdk.yml`, and the `npm`
environment, with only `npm stage publish` allowed. All workflow releases use OIDC;
do not add an npm token to GitHub.
