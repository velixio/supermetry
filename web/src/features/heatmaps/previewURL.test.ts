import { describe, expect, it } from 'vitest'

import { previewURL } from './previewURL'

describe('heatmap preview URL', () => {
  it('joins the configured host and the path', () => {
    expect(previewURL('example.com', '/pricing?plan=pro')).toBe('https://example.com/pricing?plan=pro')
    expect(previewURL('*.example.com', 'docs')).toBe('https://example.com/docs')
  })

  it('never leaves the configured host', () => {
    for (const path of ['//evil.test/x', '\\\\evil.test/x', '/\\evil.test/x', '\\/evil.test', 'https://evil.test/', '/%5C%5Cevil.test']) {
      const url = previewURL('example.com', path)
      expect(url === '' || new URL(url).host === 'example.com').toBe(true)
    }
  })

  it('refuses to frame the dashboard origin itself', () => {
    expect(previewURL('dash.test', '/', 'https://dash.test')).toBe('')
  })
})
