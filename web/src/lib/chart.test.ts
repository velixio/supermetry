import { afterEach, describe, expect, it } from 'vitest'

import { rememberInterval, rememberedInterval, resolveInterval, trimFutureBuckets } from './chart'
import { dashboardSearch } from './search'

afterEach(() => localStorage.clear())

describe('chart granularity', () => {
  it('prefers the URL, then the remembered choice, then the automatic one', () => {
    expect(resolveInterval('2026-10-01', '2026-10-30', 'week', 'hour')).toBe('week')
    expect(resolveInterval('2026-10-01', '2026-10-30', undefined, 'hour')).toBe('hour')
    expect(resolveInterval('2026-10-01', '2026-10-30')).toBe('day')
  })

  it('falls back when hourly buckets do not fit the range', () => {
    expect(resolveInterval('2025-10-27', '2026-10-26', 'hour')).toBe('month')
    expect(resolveInterval('2025-10-27', '2026-10-26', 'hour', 'week')).toBe('week')
  })

  it('remembers the last choice per site', () => {
    rememberInterval(7, 'hour')
    expect(rememberedInterval(7)).toBe('hour')
    expect(rememberedInterval(8)).toBeUndefined()
    localStorage.setItem('supermetry:granularity:9', 'fortnight')
    expect(rememberedInterval(9)).toBeUndefined()
  })

  it('keeps a valid granularity in the dashboard URL and drops an unknown one', () => {
    expect(dashboardSearch.parse({ granularity: 'hour' }).granularity).toBe('hour')
    expect(dashboardSearch.parse({ granularity: 'minute' })).not.toHaveProperty('granularity')
    expect(dashboardSearch.parse({})).not.toHaveProperty('granularity')
  })
})

describe('trimFutureBuckets', () => {
  const rows = (...times: string[]) => times.map((time) => ({ time, value: 0 }))
  // 2026-10-26 08:10 UTC is 13:40 in Kolkata (+5:30).
  const now = new Date('2026-10-26T08:10:00Z')

  it('ends hourly buckets at the current hour in the site time zone and keeps past zero hours', () => {
    const hours = rows('2026-10-26T00:00:00', '2026-10-26T12:00:00', '2026-10-26T13:00:00', '2026-10-26T14:00:00', '2026-10-26T23:00:00')
    expect(trimFutureBuckets(hours, 'hour', now, 'Asia/Kolkata').map((row) => row.time)).toEqual(['2026-10-26T00:00:00', '2026-10-26T12:00:00', '2026-10-26T13:00:00'])
    expect(trimFutureBuckets(hours, 'hour', now, 'UTC').map((row) => row.time)).toEqual(['2026-10-26T00:00:00'])
  })

  it('keeps the still-filling day, week and month bucket', () => {
    expect(trimFutureBuckets(rows('2026-10-25', '2026-10-26', '2026-10-27'), 'day', now, 'Asia/Kolkata').map((row) => row.time)).toEqual(['2026-10-25', '2026-10-26'])
    expect(trimFutureBuckets(rows('2026-10-19', '2026-10-26', '2026-11-02'), 'week', now, 'UTC').map((row) => row.time)).toEqual(['2026-10-19', '2026-10-26'])
    expect(trimFutureBuckets(rows('2026-10-01', '2026-11-01', '2026-12-01'), 'month', now, 'UTC').map((row) => row.time)).toEqual(['2026-10-01'])
  })

  it('handles the day boundary of a +5:45 zone', () => {
    // 18:30 UTC on the 25th is already 00:15 on the 26th in Kathmandu.
    const late = new Date('2026-10-25T18:30:00Z')
    expect(trimFutureBuckets(rows('2026-10-25', '2026-10-26', '2026-10-27'), 'day', late, 'Asia/Kathmandu').map((row) => row.time)).toEqual(['2026-10-25', '2026-10-26'])
  })

  it('leaves a past range alone and empties a range entirely in the future', () => {
    const past = rows('2026-09-01', '2026-09-02')
    expect(trimFutureBuckets(past, 'day', now, 'UTC')).toEqual(past)
    expect(trimFutureBuckets(rows('2026-11-01', '2026-11-02'), 'day', now, 'UTC')).toEqual([])
  })
})
