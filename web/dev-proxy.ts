// Proxy table for `make dev`: Vite serves the UI and forwards every path the Go
// server owns. Kept apart from vite.config.ts so a unit test can import it.

// Every proxied request carries this header. In dev the Go server redirects
// browser pages it does not own to Vite; without the marker, a path that Go has
// no handler for (for example /l/ with no slug) would be redirected to Vite,
// proxied straight back to Go, and loop. Go skips the redirect when it is set
// (web/devui.go keeps the same name).
export const DEV_PROXY_HEADER = 'X-Supermetry-Dev-Proxy'

const escapeRegExp = (p: string) => p.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
// Keys starting with ^ are regular expressions; the rest match by prefix.
const exact = (p: string) => `^${escapeRegExp(p)}$`

export function buildDevProxy(env: Record<string, string | undefined>) {
  const devApi = env.SUPERMETRY_DEV_API || 'http://127.0.0.1:8787'
  const trackerPath = env.SUPERMETRY_TRACKER_PATH || '/js/script.js'
  const eventPath = env.SUPERMETRY_EVENT_PATH || '/api/public/v1/events'
  const trackerDir = trackerPath.slice(0, trackerPath.lastIndexOf('/') + 1) || '/'
  const fullTrackerPath = trackerPath.replace(/\.js$/, '') + '.full.js'
  const goPaths = [
    '^/api(/|$)', // private and public APIs, replay intake, openapi.json
    exact('/healthz'),
    exact('/readyz'),
    exact('/metrics'),
    exact('/replay.js'),
    '/l/', // short links
    '/px/', // tracking pixel
    exact(trackerPath),
    exact(fullTrackerPath),
    // Per-site scripts live next to the tracker, unless it sits at the root.
    ...(trackerDir === '/' ? [] : [trackerDir]),
    exact(eventPath),
  ]
  // Origin and Cookie pass through untouched, so the server's Origin check sees the
  // browser's origin (SUPERMETRY_BASE_URL) and cookies stay first-party on the Vite host.
  return Object.fromEntries(
    [...new Set(goPaths)].map((p) => [
      p,
      { target: devApi, changeOrigin: true, headers: { [DEV_PROXY_HEADER]: '1' } },
    ]),
  )
}
