import { describe, expect, it } from 'vitest'

import { lastBucketIsPartial, noDataBucketCount, splitSeries } from './chart-ranges'

describe('noDataBucketCount', () => {
  it('marks the zero run before the first event', () => {
    expect(noDataBucketCount([0, 0, 0, 0, 4, 0, 6])).toBe(4)
  })
  it('does not mark one quiet bucket, an all-zero series or a series that starts with data', () => {
    expect(noDataBucketCount([0, 3, 4])).toBe(0)
    expect(noDataBucketCount([0, 0, 0])).toBe(0)
    expect(noDataBucketCount([5, 0, 0, 2])).toBe(0)
    expect(noDataBucketCount([])).toBe(0)
  })
  it('skips future buckets that are null', () => {
    expect(noDataBucketCount([0, 0, 2, null])).toBe(2)
  })
})

describe('lastBucketIsPartial', () => {
  const now = new Date('2026-11-18T10:30:00Z')
  it('is partial for today, the current hour and the week or month holding today', () => {
    expect(lastBucketIsPartial('2026-11-18', '2026-11-18', 'day', now, 'UTC')).toBe(true)
    expect(lastBucketIsPartial('2026-11-18T10:00:00', '2026-11-18', 'hour', now, 'UTC')).toBe(true)
    expect(lastBucketIsPartial('2026-11-16', '2026-11-18', 'week', now, 'UTC')).toBe(true)
    expect(lastBucketIsPartial('2026-11-01', '2026-11-30', 'month', now, 'UTC')).toBe(true)
  })
  it('is not partial for a range that ended earlier or when the last drawn bucket is older', () => {
    expect(lastBucketIsPartial('2026-11-10', '2026-11-10', 'day', now, 'UTC')).toBe(false)
    expect(lastBucketIsPartial('2026-11-17', '2026-11-18', 'day', now, 'UTC')).toBe(false)
    expect(lastBucketIsPartial('2026-11-18T09:00:00', '2026-11-18', 'hour', now, 'UTC')).toBe(false)
    expect(lastBucketIsPartial(undefined, '2026-11-18', 'day', now, 'UTC')).toBe(false)
  })
  it('uses the site time zone for today', () => {
    expect(lastBucketIsPartial('2026-11-19', '2026-11-19', 'day', new Date('2026-11-18T12:00:00Z'), 'Pacific/Auckland')).toBe(true)
  })
})

describe('splitSeries', () => {
  const series = [0, 0, 0, 4, 5, 2].map((value, index) => ({ time: `2026-11-0${index + 1}`, value }))
  it('draws closed buckets solid and joins the partial bucket with a dashed segment', () => {
    const split = splitSeries(series, true)
    expect(split.map((point) => point.solid)).toEqual([null, null, null, 4, 5, null])
    expect(split.map((point) => point.partial)).toEqual([null, null, null, null, 5, 2])
    expect(split[5]!.partialBucket).toBe(true)
    expect(split.slice(0, 3).every((point) => point.noData)).toBe(true)
  })
  it('keeps everything solid when no bucket is partial', () => {
    const split = splitSeries(series, false)
    expect(split.map((point) => point.partial)).toEqual([null, null, null, null, null, null])
    expect(split[5]!.solid).toBe(2)
  })
})
