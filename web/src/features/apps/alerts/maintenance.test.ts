import { describe, expect, it } from 'vitest'

import { nextOccurrences, zonedInstant } from './maintenance'

describe('nextOccurrences', () => {
  it('finds the next two Sunday 02:00 windows in Berlin (winter time)', () => {
    const out = nextOccurrences([0], '02:00', 120, 'Europe/Berlin', new Date('2026-11-18T12:00:00Z'))
    expect(out.map((o) => o.start.toISOString())).toEqual(['2026-11-22T01:00:00.000Z', '2026-11-29T01:00:00.000Z'])
    expect(out[0].end.toISOString()).toBe('2026-11-22T03:00:00.000Z')
  })

  it('follows the DST switch: 02:00 Berlin on 2027-03-28 is 01:00Z, a week later 00:00Z', () => {
    const out = nextOccurrences([0], '02:00', 120, 'Europe/Berlin', new Date('2027-03-22T00:00:00Z'))
    expect(out[0].start.toISOString()).toBe('2027-03-28T01:00:00.000Z')
    expect(out[1].start.toISOString()).toBe('2027-04-04T00:00:00.000Z')
  })

  it('handles a half-hour zone and skips a window that already started', () => {
    expect(zonedInstant(2026, 11, 22, 9, 0, 'Asia/Kolkata')).toBe(Date.parse('2026-11-22T03:30:00Z'))
    const out = nextOccurrences([0], '02:00', 60, 'UTC', new Date('2026-11-22T02:30:00Z'))
    expect(out[0].start.toISOString()).toBe('2026-11-29T02:00:00.000Z')
  })

  it('returns nothing without days or with a bad time', () => {
    expect(nextOccurrences([], '02:00', 60, 'UTC', new Date())).toEqual([])
    expect(nextOccurrences([1], 'xx', 60, 'UTC', new Date())).toEqual([])
  })
})
