import { describe, expect, it, vi } from 'vitest'

import { rehydrateCSS } from './css-rehydrate'

describe('rehydrateCSS', () => {
  it('round-trips snapshots and fetches each stylesheet once', async () => {
    const css = '.shared { color: rebeccapurple; }'
    const original = [
      {
        type: 2,
        data: {
          node: {
            type: 0,
            childNodes: [
              { type: 2, attributes: { rel: 'stylesheet', _cssText: css } },
              { type: 2, attributes: { media: 'screen', _cssText: css } },
            ],
          },
        },
        timestamp: 1,
      },
    ]
    const deduplicated = structuredClone(original)
    for (const node of deduplicated[0].data.node.childNodes) {
      delete (node.attributes as { _cssText?: string })._cssText
      Object.assign(node.attributes, { _cssRef: 'abc123' })
    }
    const fetchCSS = vi.fn().mockResolvedValue(css)

    const restored = await rehydrateCSS(deduplicated, fetchCSS)

    expect(restored).toEqual(original)
    expect(fetchCSS).toHaveBeenCalledOnce()
    expect(fetchCSS).toHaveBeenCalledWith('abc123')
  })
})
