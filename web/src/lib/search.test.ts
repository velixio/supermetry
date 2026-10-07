import { describe, expect, it } from 'vitest'

import {
  addFilter,
  dashboardSearch,
  parseFilters,
  removeFilter,
  resolveRange,
  type DashboardSearch,
} from './search'

const base: DashboardSearch = dashboardSearch.parse({})

describe('search params', () => {
  it('falls back and drops invalid filters', () => {
    const search = dashboardSearch.parse({
      period: 'bogus',
      f: ['country:is:', 'planet:is:x', 'country:is:IN'],
      compare: 'nope',
    })

    expect(search.period).toBe('30d')
    expect(search.compare).toBe('off')
    expect(parseFilters(search.f)).toEqual({
      filters: [{ dimension: 'country', operator: 'is', values: ['IN'] }],
      dropped: ['country:is:', 'planet:is:x'],
    })
  })

  it('custom range with from after to falls back to 30 days', () => {
    expect(
      resolveRange(
        'custom',
        'UTC',
        new Date('2026-10-07T12:00:00Z'),
        '2026-10-05',
        '2026-10-01',
      ),
    ).toEqual({ from: '2026-09-08', to: '2026-10-07' })
  })

  it('rejects impossible custom dates', () => {
    expect(
      resolveRange(
        'custom',
        'UTC',
        new Date('2026-03-01T12:00:00Z'),
        '2026-02-30',
        '2026-03-01',
      ),
    ).toEqual({ from: '2026-01-31', to: '2026-03-01' })
  })
})

describe('time zones', () => {
  it('today uses the site local date', () => {
    expect(
      resolveRange(
        'today',
        'Asia/Kathmandu',
        new Date('2026-10-07T20:00:00Z'),
      ),
    ).toEqual({ from: '2026-10-08', to: '2026-10-08' })
  })

  it('resolves month presets from the site-local calendar', () => {
    const now = new Date('2026-01-01T01:00:00Z')
    expect(resolveRange('month', 'America/New_York', now)).toEqual({
      from: '2025-12-01',
      to: '2025-12-31',
    })
    expect(resolveRange('last_month', 'America/New_York', now)).toEqual({
      from: '2025-11-01',
      to: '2025-11-30',
    })
  })

  it('keeps the local calendar day for the rolling 12-month preset', () => {
    expect(
      resolveRange('12mo', 'UTC', new Date('2026-10-07T12:00:00Z')),
    ).toEqual({ from: '2025-10-08', to: '2026-10-07' })
  })
})

describe('filters', () => {
  it('adds then removes a filter without losing other search state', () => {
    const added = addFilter(
      { ...base, period: '7d' },
      { dimension: 'country', operator: 'is', values: ['IN'] },
    )
    expect(added.f).toEqual(['country:is:IN'])
    expect(added.period).toBe('7d')
    expect(removeFilter(added, 0)).toEqual({ ...base, period: '7d', f: [] })
  })

  it('round-trips reserved characters and multiple values', () => {
    const added = addFilter(base, {
      dimension: 'path',
      operator: 'contains',
      values: ['/docs:a,b', '/pricing'],
    })
    expect(parseFilters(added.f).filters).toEqual([
      {
        dimension: 'path',
        operator: 'contains',
        values: ['/docs:a,b', '/pricing'],
      },
    ])
  })
})
