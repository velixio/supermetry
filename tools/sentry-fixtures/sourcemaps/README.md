# Source map fixtures

Vite app (`src/main.js`, `src/cart.js`) built with `@sentry/vite-plugin`
(debug ids injected, upload not configured, so nothing leaves the machine).
`gen.sh` builds in `/tmp/sm-fixture-app`, appends the `//# debugId=` comment and
the map `debugId` field the way `sentry-cli sourcemaps inject` does, and builds
`bundle.zip` as the TS CLI does (`SYSB` + version 2 header, flat `manifest.json`).
Output goes to `internal/sourcemap/testdata/vite/`. Pinned by `bun.lock`.
