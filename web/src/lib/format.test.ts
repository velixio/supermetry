import { describe, expect, it } from 'vitest'

import { fmt } from './format'

describe('format', () => {
  it('compacts large numbers and formats durations', () => {
    expect(fmt.number(12_345_678)).toBe('12.3M')
    expect(fmt.number(9_999)).toBe('9,999')
    expect(fmt.duration(130)).toBe('2m 10s')
    expect(fmt.duration(3_905)).toBe('1h 5m 5s')
  })

  it('formats API percentages without multiplying them', () => {
    expect(fmt.percent(42.25)).toBe('42.3%')
  })

  it('formats instants in the site time zone', () => {
    expect(fmt.date('2026-10-07T20:00:00Z', 'Asia/Kathmandu')).toBe(
      'Oct 8, 2026',
    )
  })
})
