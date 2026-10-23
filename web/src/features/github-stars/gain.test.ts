import { describe, expect, it } from 'vitest'

import { gainStats } from './gain'
import type { StarsPoint } from './githubStarsApi'

const point = (bucket: string, net: number): StarsPoint => ({ bucket, gained: Math.max(net, 0), lost: Math.max(-net, 0), net, cumulative: 0 })

describe('gainStats', () => {
  const points = [
    point('2026-10-23T09:00:00+05:30', 2),
    point('2026-10-23T10:00:00+05:30', 0),
    point('2026-10-23T11:00:00+05:30', 5),
    point('2026-10-23T12:00:00+05:30', -1),
    point('2026-10-23T13:00:00+05:30', 3),
  ]

  it('reports the current and previous bucket, the average per bucket and the peak', () => {
    // net = 2 + 0 + 5 - 1 + 3 = 9 over 5 buckets, so 1.8 per bucket; the best bucket is 11:00 with +5.
    expect(gainStats(points)).toEqual({ current: 3, previous: -1, average: 1.8, peak: { bucket: '2026-10-23T11:00:00+05:30', net: 5 } })
  })

  it('has no previous bucket or peak gain for a single quiet bucket', () => {
    expect(gainStats([point('2026-10-23T09:00:00+05:30', 0)])).toEqual({ current: 0, previous: undefined, average: 0, peak: undefined })
  })

  it('is empty without buckets', () => {
    expect(gainStats([])).toBeUndefined()
  })

  it('does not call a quiet or losing range a peak', () => {
    expect(gainStats([point('a', -2), point('b', 0)])?.peak).toBeUndefined()
  })
})
