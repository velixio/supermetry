import type { DayComparison } from './api'

export const TOLERANCE_WITHIN = 5
export const TOLERANCE_CLOSE = 15

export type Tolerance = 'within' | 'close' | 'differs' | 'none'

export function toleranceOf(diffPct: number | null): Tolerance {
  if (diffPct === null) return 'none'
  const size = Math.abs(diffPct)
  if (size <= TOLERANCE_WITHIN) return 'within'
  if (size <= TOLERANCE_CLOSE) return 'close'
  return 'differs'
}

export type MonthRow = {
  month: string
  comparedDays: number
  importedVisitors: number
  nativeVisitors: number
  importedPageviews: number
  nativePageviews: number
  visitorsDiff: number | null
  pageviewsDiff: number | null
}

function pct(imported: number, native: number): number | null {
  if (imported <= 0) return null
  return Math.round(((native - imported) / imported) * 1000) / 10
}

/** Sums the days that have both sides per calendar month (YYYY-MM). */
export function monthlyRows(days: DayComparison[]): MonthRow[] {
  const months = new Map<string, MonthRow>()
  for (const day of days) {
    if (!day.native) continue
    const key = day.day.slice(0, 7)
    const row = months.get(key) ?? {
      month: key, comparedDays: 0, importedVisitors: 0, nativeVisitors: 0,
      importedPageviews: 0, nativePageviews: 0, visitorsDiff: null, pageviewsDiff: null,
    }
    row.comparedDays += 1
    row.importedVisitors += day.imported.visitors
    row.nativeVisitors += day.native.visitors
    row.importedPageviews += day.imported.pageviews
    row.nativePageviews += day.native.pageviews
    months.set(key, row)
  }
  return [...months.values()]
    .map((row) => ({ ...row, visitorsDiff: pct(row.importedVisitors, row.nativeVisitors), pageviewsDiff: pct(row.importedPageviews, row.nativePageviews) }))
    .sort((a, b) => a.month.localeCompare(b.month))
}

/** The worse of the two metric tolerances decides the badge. */
export function monthTolerance(row: MonthRow): Tolerance {
  const order: Tolerance[] = ['none', 'within', 'close', 'differs']
  const a = toleranceOf(row.visitorsDiff)
  const b = toleranceOf(row.pageviewsDiff)
  return order.indexOf(a) >= order.indexOf(b) ? a : b
}

export function signedPercent(value: number | null, empty: string): string {
  if (value === null) return empty
  const text = `${Math.abs(value).toFixed(1)}%`
  return value > 0 ? `+${text}` : value < 0 ? `−${text}` : text
}
