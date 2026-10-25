import { afterEach, describe, expect, it } from 'vitest'

import { rememberInterval, rememberedInterval, resolveInterval } from './chart'
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
