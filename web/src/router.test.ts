import { describe, expect, it } from 'vitest'

import { parseSearch, stringifySearch } from './router'

describe('dashboard URL serialization', () => {
  it('round-trips filters as repeated plain query parameters', () => {
    const encoded = stringifySearch({
      period: '30d',
      f: ['country:is:IN', 'browser:is:Chrome'],
    })

    expect(encoded).toBe(
      '?period=30d&f=country%3Ais%3AIN&f=browser%3Ais%3AChrome',
    )
    expect(parseSearch(encoded)).toEqual({
      period: '30d',
      f: ['country:is:IN', 'browser:is:Chrome'],
    })
  })

  it('parses one filter as an array', () => {
    expect(parseSearch('?f=country%3Ais%3AIN')).toEqual({
      f: ['country:is:IN'],
    })
  })
})
