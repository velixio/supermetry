import { describe, expect, it } from 'vitest'

import { parseFilters, serializeFilters } from '@/lib/search'

import { withCohort } from './FilterBarCohorts'

describe('cohort filters', () => {
  it('replaces an earlier cohort and keeps the other filters', () => {
    const next = withCohort([
      { dimension: 'country', operator: 'is', values: ['US'] },
      { dimension: 'cohort', operator: 'is', values: ['3'] },
    ], 9)
    expect(next).toEqual([
      { dimension: 'country', operator: 'is', values: ['US'] },
      { dimension: 'cohort', operator: 'is', values: ['9'] },
    ])
  })

  it('survives the URL round trip used by dashboard links', () => {
    const encoded = serializeFilters([{ dimension: 'cohort', operator: 'is', values: ['12'] }])
    const parsed = parseFilters(encoded)
    expect(parsed.dropped).toEqual([])
    expect(parsed.filters).toEqual([{ dimension: 'cohort', operator: 'is', values: ['12'] }])
  })
})
