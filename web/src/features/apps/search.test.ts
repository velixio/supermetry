import { describe, expect, it } from 'vitest'

import { appQuery, parseAppIssuesSearch, parseAppSearch } from './search'

describe('app search', () => {
  it('defaults and drops invalid values', () => {
    expect(parseAppSearch({ period: 'nope', platform: 'beos' })).toEqual({ period: '30d', release: undefined, environment: undefined, platform: undefined })
    expect(parseAppIssuesSearch({ status: 'bogus', sort: 'events', offset: '50' })).toMatchObject({ status: 'open', sort: 'events', offset: 50 })
  })
  it('builds the API query from the range and filters', () => {
    const query = appQuery({ period: '7d', release: '1.2.0', platform: 'ios' }, 'UTC', { status: 'open', q: '' }, new Date('2026-11-15T12:00:00Z'))
    expect(query.toString()).toBe('from=2026-11-09&to=2026-11-15&release=1.2.0&platform=ios&status=open')
  })
})
