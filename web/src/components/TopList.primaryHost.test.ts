import { describe, expect, it } from 'vitest'

import { primaryHost, rowLink } from './TopList'

describe('primaryHost', () => {
  it('prefers a concrete host', () => {
    expect(primaryHost(['*.example.com', 'www.example.com'])).toBe('www.example.com')
  })

  it('falls back to the base domain of a wildcard host', () => {
    expect(primaryHost(['*.example.com'])).toBe('example.com')
    expect(rowLink('page', '/pricing', primaryHost(['*.example.com']))?.url).toBe('https://example.com/pricing')
  })

  it('has no host when the site allows any host', () => {
    expect(primaryHost([])).toBeUndefined()
    expect(primaryHost(undefined)).toBeUndefined()
  })
})
