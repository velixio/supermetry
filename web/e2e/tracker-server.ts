const port = Number(process.env.SUPERMETRY_TRACKER_E2E_PORT ?? '28149')
const tracker = Bun.file(new URL('../../internal/tracker/tracker.full.min.js', import.meta.url))

Bun.serve({
  hostname: '127.0.0.1',
  port,
  async fetch(request) {
    const url = new URL(request.url)
    if (url.pathname === '/tracker.js') {
      return new Response(await tracker.bytes(), {
        headers: { 'content-type': 'application/javascript' },
      })
    }
    if (url.pathname === '/readyz') return new Response('ok')
    return new Response('<!doctype html><title>Tracker test</title>', {
      headers: { 'content-type': 'text/html' },
    })
  },
})
