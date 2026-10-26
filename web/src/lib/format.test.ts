import { describe, expect, it } from 'vitest'

import { fmt } from './format'

describe('format', () => {
  it.each([
    [0, '0 B'],
    [1_023, '1,023 B'],
    [1_024, '1 KiB'],
    [1_536, '1.5 KiB'],
    [1_048_576, '1 MiB'],
    [1_073_741_824, '1 GiB'],
    [5_497_558_138_880, '5 TiB'],
  ])('formats %d bytes with IEC units', (value, expected) => {
    expect(fmt.bytes(value)).toBe(expected)
  })

  it('compacts large numbers and formats durations', () => {
    expect(fmt.number(12_345_678)).toBe('12.3M')
    expect(fmt.number(9_999)).toBe('9,999')
    expect(fmt.duration(130)).toBe('2m 10s')
    expect(fmt.duration(3_905)).toBe('1h 5m 5s')
    expect(fmt.duration(65)).toBe('1m 05s')
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
