import { describe, expect, it } from 'vitest'

import { buildDevProxy, DEV_PROXY_HEADER } from '../../dev-proxy'

describe('buildDevProxy', () => {
  it('marks every proxied request so Go never redirects it back to Vite', () => {
    const proxy = buildDevProxy({})
    expect(Object.keys(proxy).length).toBeGreaterThan(5)
    for (const [key, entry] of Object.entries(proxy)) {
      expect(entry.headers[DEV_PROXY_HEADER], key).toBe('1')
      expect(entry.target).toBe('http://127.0.0.1:8787')
    }
  })

  it('follows the configured paths and keeps single-file keys anchored', () => {
    const proxy = buildDevProxy({
      SUPERMETRY_DEV_API: 'http://127.0.0.1:9',
      SUPERMETRY_TRACKER_PATH: '/t.js',
      SUPERMETRY_EVENT_PATH: '/e',
    })
    const keys = Object.keys(proxy)
    expect(keys).toContain('^/t\\.js$')
    expect(keys).toContain('^/t\\.full\\.js$')
    expect(keys).toContain('^/e$')
    expect(keys).not.toContain('/')
    expect(new RegExp('^/replay\\.js$').test('/replay.json')).toBe(false)
    expect(Object.values(proxy)[0].target).toBe('http://127.0.0.1:9')
  })
})
