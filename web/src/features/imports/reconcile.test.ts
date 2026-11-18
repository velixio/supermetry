import { describe, expect, it } from 'vitest'

import type { DayComparison } from './api'
import { monthlyRows, monthTolerance, signedPercent, toleranceOf } from './reconcile'

const day = (d: string, iv: number, ip: number, nv: number | null, np = nv): DayComparison => ({
  day: d,
  imported: { visitors: iv, visits: iv, pageviews: ip },
  native: nv === null ? null : { visitors: nv, visits: nv, pageviews: np ?? 0 },
  visitors_diff_pct: null,
  pageviews_diff_pct: null,
})

describe('reconcile', () => {
  it('classifies differences by size, regardless of sign', () => {
    expect(toleranceOf(null)).toBe('none')
    expect(toleranceOf(0)).toBe('within')
    expect(toleranceOf(-5)).toBe('within')
    expect(toleranceOf(5.1)).toBe('close')
    expect(toleranceOf(-15)).toBe('close')
    expect(toleranceOf(15.1)).toBe('differs')
  })

  it('sums only days that have both sides, per month', () => {
    const rows = monthlyRows([
      day('2026-01-30', 100, 200, 98, 210),
      day('2026-01-31', 100, 200, null),
      day('2026-02-01', 50, 80, 40, 80),
    ])
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatchObject({ month: '2026-01', comparedDays: 1, importedVisitors: 100, nativeVisitors: 98, visitorsDiff: -2, pageviewsDiff: 5 })
    expect(rows[1]).toMatchObject({ month: '2026-02', visitorsDiff: -20, pageviewsDiff: 0 })
  })

  it('uses the worse metric for the month badge', () => {
    const [jan, feb] = monthlyRows([day('2026-01-01', 100, 100, 99, 120), day('2026-02-01', 100, 100, 100, 100)])
    expect(monthTolerance(jan)).toBe('differs')
    expect(monthTolerance(feb)).toBe('within')
  })

  it('formats signed percentages with a real minus', () => {
    expect(signedPercent(null, 'n/a')).toBe('n/a')
    expect(signedPercent(2.34, '-')).toBe('+2.3%')
    expect(signedPercent(-2.34, '-')).toBe('−2.3%')
    expect(signedPercent(0, '-')).toBe('0.0%')
  })
})
