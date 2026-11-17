# Upload source maps

Readable JavaScript stack traces need your source maps. Supermetry accepts them
from the standard Sentry upload tools; nothing but environment variables changes.

## 1. Create an upload token

In the app project's **Settings, Installation**, open **Upload source maps** and
create an upload token. It can only upload source maps and manage releases for
that project, and it already contains your server address and organization, so it
is the only secret you set. Copy it once; it is not shown again.

## 2. Inject debug IDs and upload

With the Rust `sentry-cli` (npm package `@sentry/cli`):

```sh
export SENTRY_AUTH_TOKEN=<your upload token>
export SENTRY_PROJECT=<project id>
npx @sentry/cli sourcemaps inject ./dist
npx @sentry/cli sourcemaps upload --release <release> ./dist
```

`SENTRY_URL` is only needed with a plain API key (scope `releases:write`) instead
of an upload token; set it to your server's address without a path.

With `@sentry/vite-plugin` (other Sentry bundler plugins work the same way), set
`url` and `authToken` in the plugin options. The plugin injects debug IDs and
uploads during the build. Set the same `release` your SDK reports.

The TypeScript CLI (npm package `sentry`) uses the same variables:
`sentry sourcemap inject ./dist` and `sentry sourcemap upload --release <release> ./dist`.

Debug IDs match a frame to its map exactly. Without them, Supermetry falls back to
the release and the file URL. Keep `release` in `Sentry.init` identical to the
release you upload.

## Tested versions

`make sdk-compat-sourcemaps` uploads a Vite build with each tool against a real
server and checks the upload. Tested: `@sentry/cli` 3.8.2, `@sentry/vite-plugin`
4.6.1, `sentry` (TypeScript CLI) 0.49.0.
