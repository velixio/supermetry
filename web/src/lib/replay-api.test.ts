import { describe, expect, it } from 'vitest'

import { parseReplayNDJSON, normalizeReplaySettings } from './replay-api'

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
