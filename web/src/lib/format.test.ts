import { afterEach, describe, expect, it } from 'vitest'

import { activeLocale, switchLocale } from '@/lib/i18n'
import { fmt } from './format'

afterEach(async () => {
  await switchLocale('en')
})

describe('format', () => {
  it.each([
    [0, '0 B'],
    [1_023, '1,023 B'],
    [1_024, '1 KiB'],
    [1_536, '1.5 KiB'],
    [1_048_576, '1 MiB'],
    [1_073_741_824, '1 GiB'],
    [5_497_558_138_880, '5 TiB'],
    // Unit is chosen after rounding: these used to show 1,024 KiB / 1,024 MiB.
    [1_048_575, '1 MiB'],
    [1_047_552, '1,023 KiB'],
    [1_073_741_823, '1 GiB'],
    [10_239, '10 KiB'],
    [1_023.5, '1 KiB'],
  ])('formats %d bytes with IEC units', (value, expected) => {
    expect(fmt.bytes(value)).toBe(expected)
  })

  it('localizes the number and the unit label', async () => {
    await switchLocale('de')
    expect(fmt.bytes(1_536)).toBe('1,5 KiB')
    expect(fmt.bytes(1_048_575)).toBe('1 MiB')
    await switchLocale('bn')
    const oneAndAHalf = new Intl.NumberFormat(activeLocale(), { maximumFractionDigits: 1 }).format(1.5)
    expect(fmt.bytes(1_536)).toBe(`${oneAndAHalf} কিবিবাইট`)
    expect(fmt.bytes(1_048_575)).toContain('মেবিবাইট')
    expect(fmt.bytes(1_048_575)).not.toContain('KiB')
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
