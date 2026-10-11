export const weekdays = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'] as const

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

export function trafficCells(rows: TrafficRow[], metric: TrafficMetric): TrafficCell[] {
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

  return Array.from({ length: 24 }, (_, hour) =>
    Array.from({ length: 7 }, (_, weekday) => ({
      weekday,
      hour,
      value: values.get(`${weekday}:${hour}`) ?? 0,
    })),
  ).flat()
}

export function peakTrafficCell(cells: TrafficCell[]): TrafficCell | undefined {
  let peak: TrafficCell | undefined
  for (const cell of cells) {
    if (cell.value > 0 && (!peak || cell.value > peak.value)) peak = cell
  }
  return peak
}

export function dotScale(value: number, maximum: number): { diameter: number; opacity: number } {
  if (!Number.isFinite(value) || !Number.isFinite(maximum) || value <= 0 || maximum <= 0) {
    return { diameter: 6, opacity: 0.2 }
  }
  const ratio = Math.min(1, value / maximum)
  return {
    diameter: Math.max(6, 18 * Math.sqrt(ratio)),
    opacity: 0.2 + 0.8 * ratio,
  }
}
