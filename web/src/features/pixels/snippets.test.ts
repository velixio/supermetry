import { describe, expect, it } from 'vitest'

import { badgeUrl, pixelSnippets } from './snippets'

describe('pixelSnippets', () => {
  const p = { svg_url: 'https://sm.example/px/pAAAAAAAAAAA.svg', gif_url: 'https://sm.example/px/pAAAAAAAAAAA.gif' }

  it('links the badge to the homepage by default and offers unlinked variants', () => {
    const s = pixelSnippets(p, 'https://supermetry.com/')
    expect(s.linkedMarkdown).toBe('[![views](https://sm.example/px/pAAAAAAAAAAA.svg)](https://supermetry.com/?ref=badge)')
    expect(s.statsMarkdown).toBe('[![views](https://sm.example/px/pAAAAAAAAAAA.svg?style=stats)](https://supermetry.com/?ref=badge)')
    expect(s.markdown).toBe('![views](https://sm.example/px/pAAAAAAAAAAA.svg)')
    expect(s.invisibleMarkdown).toBe('![](https://sm.example/px/pAAAAAAAAAAA.gif)')
    expect(s.html).toBe('<img src="https://sm.example/px/pAAAAAAAAAAA.gif" alt="" width="1" height="1" referrerpolicy="no-referrer-when-downgrade">')
  })

  it('adds badge styles after an existing tracking tag', () => {
    const s = pixelSnippets({ svg_url: `${p.svg_url}?c=spring`, gif_url: `${p.gif_url}?c=spring` }, 'https://supermetry.com')
    expect(s.statsMarkdown).toContain('?c=spring&style=stats')
  })
})

describe('badgeUrl', () => {
  it('swaps .gif for .svg and keeps the tracking tag', () => {
    expect(badgeUrl('https://sm.example/px/siteAAAA0001.gif?c=spring')).toBe('https://sm.example/px/siteAAAA0001.svg?c=spring')
  })
})
