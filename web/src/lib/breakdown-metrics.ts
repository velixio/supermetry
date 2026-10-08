import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

type BreakdownRow = Record<string, unknown>

export function metricLabel(metric: string): string {
  if (metric === 'time_on_page') return en.dashboard.metrics.timeOnPage
  if (metric === 'exit_rate') return en.dashboard.metrics.exitRate
  return metric
    .split('_')
    .map((part) => `${part.slice(0, 1).toUpperCase()}${part.slice(1)}`)
    .join(' ')
}

export function metricIsUndefined(metric: string, row: BreakdownRow): boolean {
  if (metric === 'time_on_page') return Number(row.time_on_page ?? 0) <= 0
  if (metric === 'exit_rate') return Number(row.pageviews ?? 0) <= 0
  return false
}

export function formatBreakdownMetric(metric: string, row: BreakdownRow): string {
  if (metricIsUndefined(metric, row)) return '—'
  const value = Number(row[metric] ?? 0)
  if (metric === 'time_on_page') return fmt.duration(value)
  if (metric === 'exit_rate') return fmt.percent(value)
  return fmt.number(value)
}

export function undefinedLast(
  rows: BreakdownRow[],
  metric: string | undefined,
): BreakdownRow[] {
  if (!metric || (metric !== 'time_on_page' && metric !== 'exit_rate')) return rows
  return [...rows].sort((left, right) =>
    Number(metricIsUndefined(metric, left)) - Number(metricIsUndefined(metric, right)),
  )
}
