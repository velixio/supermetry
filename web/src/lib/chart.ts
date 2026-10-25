export type ChartInterval = 'hour' | 'day' | 'week' | 'month'

export const chartIntervals: readonly ChartInterval[] = ['hour', 'day', 'week', 'month']

export const maxHourlyRangeDays = 30

export function rangeLengthDays(from: string, to: string): number {
  return Math.floor((new Date(`${to}T00:00:00Z`).getTime() - new Date(`${from}T00:00:00Z`).getTime()) / 86_400_000) + 1
}

export function autoInterval(from: string, to: string): ChartInterval {
  const days = rangeLengthDays(from, to)
  if (days <= 2) return 'hour'
  if (days <= 92) return 'day'
  if (days <= 183) return 'week'
  return 'month'
}

export function isChartInterval(value: unknown): value is ChartInterval {
  return typeof value === 'string' && (chartIntervals as readonly string[]).includes(value)
}

/** Hourly buckets are limited to 30-day ranges; every other granularity fits any range. */
export function intervalAllowed(interval: ChartInterval, from: string, to: string): boolean {
  return interval !== 'hour' || rangeLengthDays(from, to) <= maxHourlyRangeDays
}

/**
 * The granularity to draw: the URL's choice, else the one last used for the site, else the
 * automatic one for the range. A choice that does not fit the range (Hour on 12 months) falls
 * back without rewriting the URL, so it comes back when the range allows it again.
 */
export function resolveInterval(from: string, to: string, requested?: ChartInterval, remembered?: ChartInterval): ChartInterval {
  for (const candidate of [requested, remembered]) {
    if (candidate && intervalAllowed(candidate, from, to)) return candidate
  }
  return autoInterval(from, to)
}

const storageKey = (siteId: number) => `supermetry:granularity:${siteId}`

export function rememberedInterval(siteId: number): ChartInterval | undefined {
  try {
    const value = globalThis.localStorage?.getItem(storageKey(siteId))
    return isChartInterval(value) ? value : undefined
  } catch {
    return undefined
  }
}

export function rememberInterval(siteId: number, interval: ChartInterval): void {
  try {
    globalThis.localStorage?.setItem(storageKey(siteId), interval)
  } catch {
    // Storage may be unavailable (private mode); the URL still carries the choice.
  }
}
