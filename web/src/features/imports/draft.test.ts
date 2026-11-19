import { describe, expect, it } from 'vitest'

import { addDays, clearDraft, dayCount, defaultDraft, loadDraft, previewRange, pullBody, rangeProblem, saveDraft, sourceReady } from './draft'

describe('draft', () => {
  it('counts days inclusively and rejects reversed ranges', () => {
    expect(dayCount('2026-01-01', '2026-01-01')).toBe(1)
    expect(dayCount('2026-01-01', '2026-01-31')).toBe(31)
    expect(dayCount('2026-02-01', '2026-01-01')).toBe(0)
    expect(addDays('2026-03-01', -1)).toBe('2026-02-28')
  })

  it('validates the range', () => {
    const now = new Date('2026-06-10T12:00:00Z')
    expect(rangeProblem({ from: 'x', to: '2026-01-01' }, now)).toBe('invalid')
    expect(rangeProblem({ from: '2000-01-01', to: '2026-01-01' }, now)).toBe('tooLong')
    expect(rangeProblem({ from: '2026-06-01', to: '2026-06-11' }, now)).toBe('future')
    expect(rangeProblem({ from: '2026-06-01', to: '2026-06-10' }, now)).toBeNull()
  })

  it('requires the fields each source needs', () => {
    const base = defaultDraft(new Date('2026-06-10T00:00:00Z'))
    expect(base.to).toBe('2026-06-09')
    expect(sourceReady(base)).toBe(false)
    expect(sourceReady({ ...base, remoteSiteId: 'example.com', apiKey: 'k' })).toBe(true)
    expect(sourceReady({ ...base, apiMethod: 'matomo_api', baseUrl: 'https://m.example', remoteSiteId: 'abc', tokenAuth: 't' })).toBe(false)
    expect(sourceReady({ ...base, apiMethod: 'matomo_api', baseUrl: 'https://m.example', remoteSiteId: '4', tokenAuth: 't' })).toBe(true)
    expect(sourceReady({ ...base, apiMethod: 'ga4_api', connectionId: 2, property: 'properties/1' })).toBe(true)
    expect(sourceReady({ ...base, mode: 'file' })).toBe(false)
  })

  it('builds the request body per method', () => {
    const base = defaultDraft(new Date('2026-06-10T00:00:00Z'))
    expect(pullBody({ ...base, remoteSiteId: ' a.com ', apiKey: ' k ' })).toMatchObject({ method: 'plausible_api', remote_site_id: 'a.com', api_key: 'k' })
    expect(pullBody({ ...base, apiMethod: 'ga4_api', connectionId: 3, property: 'properties/9' })).toEqual({
      method: 'ga4_api', from: base.from, to: base.to, property: 'properties/9', connection_id: 3,
    })
  })

  it('previews only the most recent 120 days of a long range', () => {
    expect(previewRange({ from: '2026-01-01', to: '2026-02-01' })).toMatchObject({ partial: false, total: 32 })
    const long = previewRange({ from: '2025-01-01', to: '2025-12-31' })
    expect(long).toMatchObject({ partial: true, total: 365, to: '2025-12-31' })
    expect(dayCount(long.from, long.to)).toBe(120)
  })

  it('persists non-secret choices only', () => {
    saveDraft(5, { ...defaultDraft(), apiKey: 'secret', tokenAuth: 'secret2', remoteSiteId: 'a.com' })
    const raw = window.sessionStorage.getItem('supermetry:import-draft:5') ?? ''
    expect(raw).not.toContain('secret')
    expect(loadDraft(5)?.remoteSiteId).toBe('a.com')
    clearDraft(5)
    expect(loadDraft(5)).toBeUndefined()
  })
})
