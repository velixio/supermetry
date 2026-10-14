import { describe, expect, it } from 'vitest'

import { bucketViews } from './buckets'

const daily = [
  { day: '2026-09-30', proxy: 1, direct: 1 }, // Wednesday
  { day: '2026-10-04', proxy: 2, direct: 0 }, // Sunday, same week as 09-30
  { day: '2026-10-05', proxy: 4, direct: 3 }, // Monday starts a new week
]

describe('bucketViews', () => {
  it('keeps days as they are', () => expect(bucketViews(daily, 'day')).toBe(daily))
  it('groups Monday-start weeks', () => {
    expect(bucketViews(daily, 'week')).toEqual([{ day: '2026-09-28', proxy: 3, direct: 1 }, { day: '2026-10-05', proxy: 4, direct: 3 }])
  })
  it('groups calendar months', () => {
    expect(bucketViews(daily, 'month')).toEqual([{ day: '2026-09-01', proxy: 1, direct: 1 }, { day: '2026-10-01', proxy: 6, direct: 3 }])
  })
})
