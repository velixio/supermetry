import { beforeEach, describe, expect, it, vi } from 'vitest'

const { del, get, put } = vi.hoisted(() => ({ del: vi.fn(), get: vi.fn(), put: vi.fn() }))
vi.mock('./api', () => ({ api: { DELETE: del, GET: get, PUT: put } }))

import { listReplays, normalizeReplaySettings, parseReplayNDJSON, replaySettingsBody, setReplaySaved } from './replay-api'

beforeEach(() => vi.clearAllMocks())

describe('parseReplayNDJSON', () => {
  it('keeps complete events and marks an invalid final line as truncated', () => {
    expect(parseReplayNDJSON('{"type":4,"timestamp":1}\n{"type":2')).toEqual({
      events: [{ type: 4, timestamp: 1 }],
      truncated: true,
    })
  })

  it('rejects malformed lines before the tail', () => {
    expect(() => parseReplayNDJSON('{bad}\n{"type":4,"timestamp":1}\n')).toThrow('Invalid replay event')
  })
})

describe('normalizeReplaySettings', () => {
  it('turns null url_patterns into an empty list', () => {
    const settings = normalizeReplaySettings({
      enabled: true,
      mask: { value: false, locked: false },
      sample_rate: { value: 15, locked: false },
      retention_days: { value: 30, locked: false },
      quota_bytes: { value: 5368709120, locked: false },
      triggers: { error: true, rage_click: true, url: true, manual: true, url_patterns: null as unknown as string[] },
      consent: 'auto',
    })
    expect(settings.triggers.url_patterns).toEqual([])
  })
})

describe('replaySettingsBody', () => {
  it('sends project opt-in and numeric constraints, preserving required masking', () => {
    const body = replaySettingsBody({
      enabled: true,
      configured_enabled: true,
      instance_enabled: true,
      mask: { value: true, locked: true },
      sample_rate: { value: 20, locked: false },
      retention_days: { value: 30, locked: true },
      quota_bytes: { value: 1024, locked: false },
      triggers: { error: true, rage_click: true, url: true, manual: true, url_patterns: [] },
      consent: 'auto',
    })
    expect(body).not.toHaveProperty('mask')
    expect(body).toHaveProperty('retention_days', 30)
    expect(body).toHaveProperty('enabled', true)
    expect(body).toMatchObject({ sample_rate: 20, quota_bytes: 1024, consent: 'auto' })
  })
})

describe('replay triage API', () => {
  it('sends every list filter and preserves string session IDs', async () => {
    get.mockResolvedValue({
      data: { data: [{ session_id: '1683029196918025731', started_at: '2026-10-08T10:00:00Z', duration_s: 30, pages: 1, bytes: 10, trigger: '', browser: '', os: '', device: '', country: '', city: '', actions: 1, saved: true }], total: 1, total_capped: false },
      response: new Response(null, { status: 200 }),
    })
    const query = { from: '2026-10-01', to: '2026-10-08', q: 'checkout', min_duration: 30, max_duration: 90, has_events: true, saved: true, cursor: 'next', limit: 50 }

    const result = await listReplays(7, query)

    expect(get).toHaveBeenCalledWith('/api/private/sites/{site_id}/replays', {
      params: { path: { site_id: 7 }, query }, signal: undefined,
    })
    expect(result.items[0]).toMatchObject({ session_id: '1683029196918025731', saved: true })
  })

  it('uses idempotent saved-resource methods', async () => {
    put.mockResolvedValue({ response: new Response(null, { status: 204 }) })
    del.mockResolvedValue({ response: new Response(null, { status: 204 }) })

    await setReplaySaved(7, '401', true)
    await setReplaySaved(7, '401', false)

    expect(put).toHaveBeenCalledWith('/api/private/sites/{site_id}/replays/{session_id}/saved', { params: { path: { site_id: 7, session_id: '401' } } })
    expect(del).toHaveBeenCalledWith('/api/private/sites/{site_id}/replays/{session_id}/saved', { params: { path: { site_id: 7, session_id: '401' } } })
  })
})
