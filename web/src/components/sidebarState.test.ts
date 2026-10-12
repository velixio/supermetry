import { describe, expect, it } from 'vitest'

import { SIDEBAR_EXPANDED_MIN_WIDTH, sidebarStartsOpen } from './sidebarState'

describe('sidebarStartsOpen', () => {
  it('expands from 1280px', () => {
    expect(SIDEBAR_EXPANDED_MIN_WIDTH).toBe(1280)
  })

  it.each([
    ['', 1440, true],
    ['', 1280, true],
    ['', 1279, false],
    ['', 1024, false],
    ['', 768, false],
    ['sidebar_state=true', 1024, true],
    ['sidebar_state=false', 1440, false],
    ['theme=dark; sidebar_state=true', 800, true],
    ['sidebar_state=garbage', 1440, true],
    ['sidebar_state=garbage', 1024, false],
    ['other_sidebar_state=true', 1024, false],
    ['other_sidebar_state=false; sidebar_state=true', 1024, true],
  ])('cookie %j at %ipx starts open=%s', (cookie, width, open) => {
    expect(sidebarStartsOpen(cookie, width)).toBe(open)
  })
})
