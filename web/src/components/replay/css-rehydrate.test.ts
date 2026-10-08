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

  it('plays without a stylesheet that cannot be fetched instead of failing the replay', async () => {
    const events = [
      { type: 2, data: { node: { attributes: { _cssRef: 'missing' }, childNodes: [{ attributes: { _cssRef: 'ok' } }] } } },
    ]
    const result = await rehydrateCSS(events, async (hash) => {
      if (hash === 'missing') throw new Error('Request failed (404)')
      return 'body{color:red}'
    })
    const root = result[0].data.node as { attributes: Record<string, unknown>; childNodes: { attributes: Record<string, unknown> }[] }
    expect(root.attributes).not.toHaveProperty('_cssRef')
    expect(root.attributes).not.toHaveProperty('_cssText')
    expect(root.childNodes[0].attributes._cssText).toBe('body{color:red}')
  })
})
