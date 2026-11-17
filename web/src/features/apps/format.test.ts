import { describe, expect, it } from 'vitest'

import { safeGitHubUrl } from './format'

describe('safeGitHubUrl', () => {
  it('accepts https github.com links only', () => {
    expect(safeGitHubUrl('https://github.com/o/r/issues/1')).toBe('https://github.com/o/r/issues/1')
    for (const bad of [
      'javascript:alert(1)', 'http://github.com/o/r/issues/1', 'https://github.com.evil.com/o/r/issues/1',
      'https://evil.com/github.com/o/r/issues/1', 'https://user\u0040github.com/o/r/issues/1', 'data:text/html,x', 'not a url', '', null, undefined,
    ]) expect(safeGitHubUrl(bad)).toBeNull()
  })
})
