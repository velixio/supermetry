export type TrafficMetric = 'visitors' | 'pageviews'

export type TrafficCell = {
  weekday: number
  hour: number
  value: number
}

type TrafficRow = {
  weekday?: unknown
  hour_of_day?: unknown
  visitors?: unknown
  pageviews?: unknown
}

export type OrderedWeekday = { index: number; name: string; short: string }

function localeFirstDay(locale?: string): number {
  try {
    const value = new Intl.Locale(locale ?? new Intl.DateTimeFormat().resolvedOptions().locale)
    const localeWithWeek = value as Intl.Locale & {
      weekInfo?: { firstDay: number }
      getWeekInfo?: () => { firstDay: number }
    }
    const info = localeWithWeek.weekInfo ?? localeWithWeek.getWeekInfo?.()
    if (info && Number.isInteger(info.firstDay)) return info.firstDay % 7
  } catch {
    // Invalid or unsupported locales use the documented Monday fallback.
  }
  return 1
}

export function orderedWeekdays(locale?: string): OrderedWeekday[] {
  const first = localeFirstDay(locale)
  const long = new Intl.DateTimeFormat(locale, { weekday: 'long', timeZone: 'UTC' })
  const short = new Intl.DateTimeFormat(locale, { weekday: 'short', timeZone: 'UTC' })
  return Array.from({ length: 7 }, (_, offset) => {
    const index = (first + offset) % 7
    const date = new Date(Date.UTC(2023, 0, 1 + index))
    return { index, name: long.format(date), short: short.format(date) }
  })
}

export function trafficCells(rows: TrafficRow[], metric: TrafficMetric, locale?: string): TrafficCell[] {
  const values = new Map<string, number>()
  for (const row of rows) {
    const weekday = Number(row.weekday)
    const hour = Number(row.hour_of_day)
    const value = Number(row[metric])
    if (!Number.isInteger(weekday) || weekday < 0 || weekday > 6) continue
    if (!Number.isInteger(hour) || hour < 0 || hour > 23) continue
    if (!Number.isFinite(value) || value <= 0) continue
    values.set(`${weekday}:${hour}`, value)
  }

  return orderedWeekdays(locale).flatMap(({ index: weekday }) =>
    Array.from({ length: 24 }, (_, hour) => ({
      weekday,
      hour,
      value: values.get(`${weekday}:${hour}`) ?? 0,
    })),
  )
}

export function marginTotals(cells: TrafficCell[]): { weekday: number[]; hour: number[] } {
  const weekday = Array.from({ length: 7 }, () => 0)
  const hour = Array.from({ length: 24 }, () => 0)
  cells.forEach((cell, index) => {
    weekday[Math.floor(index / 24)] += cell.value
    hour[cell.hour] += cell.value
  })
  return { weekday, hour }
}

export function peakTrafficCell(cells: TrafficCell[]): TrafficCell | undefined {
  let peak: TrafficCell | undefined
  for (const cell of cells) {
    if (cell.value > 0 && (!peak || cell.value > peak.value)) peak = cell
  }
  return peak
}

export function peakTrafficWindows(cells: TrafficCell[], limit = 3): TrafficCell[] {
  return cells
    .map((cell, index) => ({ cell, index }))
    .filter(({ cell }) => cell.value > 0)
    .sort((left, right) => right.cell.value - left.cell.value || left.index - right.index)
    .slice(0, limit)
    .map(({ cell }) => cell)
}

export function weekpartShares(cells: TrafficCell[]): { weekdays: number; weekends: number } {
  const total = cells.reduce((sum, cell) => sum + cell.value, 0)
  if (total === 0) return { weekdays: 0, weekends: 0 }
  const weekdays = cells.reduce((sum, cell) => sum + (cell.weekday >= 1 && cell.weekday <= 5 ? cell.value : 0), 0)
  const weekdayShare = Math.round((weekdays / total) * 100)
  return { weekdays: weekdayShare, weekends: 100 - weekdayShare }
}

export function quietestWeekday(cells: TrafficCell[]): { weekday: number; value: number } | undefined {
  const totals = marginTotals(cells).weekday
  if (totals.every((value) => value === 0)) return undefined
  let quietestIndex = 0
  for (let index = 1; index < totals.length; index += 1) {
    if (totals[index] < totals[quietestIndex]) quietestIndex = index
  }
  return { weekday: cells[quietestIndex * 24]?.weekday ?? quietestIndex, value: totals[quietestIndex] }
}
