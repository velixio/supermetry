// previewURL builds the iframe source from the site's configured host only.
// The path comes from the URL search string, so the result is rejected unless
// it stays on that host; the dashboard's own origin is never framed because
// allow-scripts plus allow-same-origin would let it lift its sandbox.
export function previewURL(host: string, path: string, dashboardOrigin = globalThis.location?.origin ?? ''): string {
  host = host.replace(/^\*\./, '')
  if (host === '') return ''
  try {
    const base = new URL(/^https?:\/\//i.test(host) ? host : `https://${host}`)
    const url = new URL(`/${path.replace(/^[/\\]+/, '')}`, base.origin)
    if (url.origin !== base.origin || url.origin === dashboardOrigin) return ''
    return url.toString()
  } catch {
    return ''
  }
}
