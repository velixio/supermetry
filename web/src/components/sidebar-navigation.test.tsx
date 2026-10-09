import { describe, expect, it } from 'vitest'
import { visibleNavigation } from './Sidebar'

describe('primary sidebar navigation', () => {
  it('keeps settings out of analytics navigation for every role and site state', () => {
    for (const role of ['owner', 'admin', 'editor', 'viewer'] as const) {
      for (const hasSite of [true, false]) {
        expect(visibleNavigation(role, hasSite).some((item) => /settings/i.test(item.label))).toBe(false)
      }
    }
  })
})
