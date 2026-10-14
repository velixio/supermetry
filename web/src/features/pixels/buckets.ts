export type Granularity = 'day' | 'week' | 'month'
export type DayViews = { day: string; proxy: number; direct: number }

function weekStart(day: string): string {
  const date = new Date(`${day}T00:00:00Z`)
  date.setUTCDate(date.getUTCDate() - ((date.getUTCDay() + 6) % 7))
  return date.toISOString().slice(0, 10)
}

/** Groups daily rows into days, Monday-start weeks or calendar months; keys are the first day of each bucket. */
export function bucketViews(daily: DayViews[], granularity: Granularity): DayViews[] {
  if (granularity === 'day') return daily
  const buckets = new Map<string, DayViews>()
  for (const row of daily) {
    const key = granularity === 'week' ? weekStart(row.day) : `${row.day.slice(0, 7)}-01`
    const bucket = buckets.get(key) ?? { day: key, proxy: 0, direct: 0 }
    bucket.proxy += row.proxy
    bucket.direct += row.direct
    buckets.set(key, bucket)
  }
  return [...buckets.values()].sort((a, b) => a.day.localeCompare(b.day))
}
