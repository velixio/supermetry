import { describe, expect, it } from 'vitest'

import { parseReplayNDJSON } from './replay-api'

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
