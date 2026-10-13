import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  addFilter,
  dashboardSearch,
  defaultSearch,
  parseDashboardSearch,
  parseFilters,
  removeFilter,
  replaceFilters,
  resolveRange,
  serializeFilters,
  type DashboardSearch,
} from './search'

const base: DashboardSearch = dashboardSearch.parse({})

describe('search params', () => {
  it.each(['exit_rate', 'time_on_page'] as const)(
    'accepts the %s metric in dashboard URLs',
    (metric) => {
      expect(dashboardSearch.parse({ metric }).metric).toBe(metric)
    },
  )

  it('falls back to visitors for an unknown metric', () => {
    expect(dashboardSearch.parse({ metric: 'unknown' }).metric).toBe('visitors')
  })

  it.each(['28d', '91d', '6mo'] as const)(
    'accepts the %s period in existing URLs',
    (period) => {
      expect(dashboardSearch.parse({ period }).period).toBe(period)
    },
  )

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

  it.each([
    ['28d', { from: '2026-08-04', to: '2026-08-31' }],
    ['91d', { from: '2026-06-02', to: '2026-08-31' }],
    ['6mo', { from: '2026-03-01', to: '2026-08-31' }],
  ] as const)('resolves %s through a fixed month-end', (period, expected) => {
    expect(
      resolveRange(period, 'UTC', new Date('2026-08-31T12:00:00Z')),
    ).toEqual(expected)
  })

  it('clamps the rolling 6-month boundary across leap day', () => {
    expect(
      resolveRange('6mo', 'UTC', new Date('2024-02-29T12:00:00Z')),
    ).toEqual({ from: '2023-08-30', to: '2024-02-29' })
  })
})

describe('filters', () => {
  it.each([
    ['browser_version:is:Chrome%20126', 'browser_version', 'Chrome 126'],
    ['os_version:is:Windows%2010%2F11', 'os_version', 'Windows 10/11'],
    ['screen_size:is:mobile', 'screen_size', 'mobile'],
    ['language:is:en', 'language', 'en'],
    ['hostname:is:docs.example.com', 'hostname', 'docs.example.com'],
  ])('keeps the new dimension filter %s', (raw, dimension, value) => {
    expect(parseFilters([raw])).toEqual({
      filters: [{ dimension, operator: 'is', values: [value] }],
      dropped: [],
    })
  })

  it('keeps channel filters', () => {
    expect(
      parseFilters(
        dashboardSearch.parse({ f: ['channel:is:AI%20Assistants'] }).f,
      ),
    ).toEqual({
      filters: [
        {
          dimension: 'channel',
          operator: 'is',
          values: ['AI Assistants'],
        },
      ],
      dropped: [],
    })
  })

  it('serializes a filter set canonically for saved segments', () => {
    expect(
      serializeFilters([
        { dimension: 'country', operator: 'is', values: ['IN', 'US'] },
        { dimension: 'path', operator: 'contains', values: ['/docs:a,b'] },
      ]),
    ).toEqual(['country:is:IN,US', 'path:contains:%2Fdocs%3Aa%2Cb'])
  })

  it('accepts region and city filters', () => {
    expect(
      parseFilters(['region:is:California', 'city:is:San%20Francisco']).filters,
    ).toEqual([
      { dimension: 'region', operator: 'is', values: ['California'] },
      { dimension: 'city', operator: 'is', values: ['San Francisco'] },
    ])
  })

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

  it('replaces only filters when applying a segment', () => {
    const search: DashboardSearch = {
      ...base,
      period: 'custom',
      from: '2026-10-01',
      to: '2026-10-07',
      compare: 'previous_period',
      metric: 'bounce_rate',
      live: true,
      f: ['country:is:US'],
    }
    expect(
      replaceFilters(search, [{ dimension: 'country', operator: 'is_not', values: ['IN'] }]),
    ).toEqual({ ...search, f: ['country:is_not:IN'] })
  })
})

describe('defaultSearch', () => {
  it('equals what the dashboard parses from an empty query', () => {
    expect(defaultSearch()).toEqual(parseDashboardSearch({}))
  })

  it('returns a fresh filter array on every call', () => {
    const first = defaultSearch()
    first.f.push('x')
    expect(defaultSearch().f).toEqual([])
  })

  it('is the only place that spells out the default analytics search', () => {
    const web = join(__dirname, '..', '..')
    const literal = ["period: '30d'", "compare: 'off'", 'f: []', "metric: 'visitors'", 'include_imported: true'].join(', ')
    const files = (dir: string): string[] => readdirSync(dir).flatMap((name) => {
      const path = join(dir, name)
      if (statSync(path).isDirectory()) return files(path)
      return /\.tsx?$/.test(name) && !/\.test\./.test(name) ? [path] : []
    })
    const offenders = files(join(web, 'src'))
      .filter((file) => readFileSync(file, 'utf8').includes(literal))
      .map((file) => relative(web, file))
    expect(offenders).toEqual(['src/lib/search.ts'])
  })
})
