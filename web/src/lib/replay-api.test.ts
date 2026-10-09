import { describe, expect, it } from 'vitest'

import { normalizeReplaySettings, parseReplayNDJSON, replaySettingsBody } from './replay-api'

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
