// Extra Host headers the Vite dev server accepts in `make dev`. The Go server
// forwards the client's Host unchanged, so Vite's DNS-rebinding protection
// (server.allowedHosts) sees the name the browser used. By default only localhost
// is allowed; this adds the hostname of SUPERMETRY_BASE_URL and any names listed
// in SUPERMETRY_DEV_ALLOWED_HOSTS. The check itself is never disabled.

const LOOPBACK = new Set(['localhost', '127.0.0.1', '::1'])

function baseUrlHost(raw: string | undefined): string | null {
  if (!raw) return null
  try {
    const host = new URL(raw).hostname.toLowerCase().replace(/^\[|\]$/g, '')
    return host && !LOOPBACK.has(host) ? host : null
  } catch {
    return null
  }
}

export function devAllowedHosts(env: Record<string, string | undefined>): string[] {
  const hosts: string[] = []
  const add = (host: string | null) => {
    if (host && !hosts.includes(host)) hosts.push(host)
  }
  add(baseUrlHost(env.SUPERMETRY_BASE_URL))
  let refused = false
  for (const part of (env.SUPERMETRY_DEV_ALLOWED_HOSTS ?? '').split(',')) {
    const host = part.trim()
    if (!host) continue
    // `true` (and a bare `*`) would switch Vite's host check off entirely.
    if (host === '*' || host.toLowerCase() === 'true') {
      refused = true
      continue
    }
    add(host)
  }
  if (refused) {
    console.warn('SUPERMETRY_DEV_ALLOWED_HOSTS: ignoring "true"/"*"; list host names instead (use .supermetry.com for subdomains).')
  }
  return hosts
}
