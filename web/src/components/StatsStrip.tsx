import { ErrorState } from './ErrorState'
import { Button } from '@/components/ui/button'

import { fmt } from '../lib/format'
import type { Compare } from '../lib/search'
import { useStats, type StatsQuery } from '../lib/stats'

export const statMetrics = [
  'visitors',
  'visits',
  'pageviews',
  'views_per_visit',
  'bounce_rate',
  'visit_duration',
  'engagement_time',
] as const satisfies readonly StatsQuery['metrics'][number][]

export type StatMetric = (typeof statMetrics)[number]
export const isStatMetric = (value: string): value is StatMetric => (statMetrics as readonly string[]).includes(value)

type StatsStripProps = {
  query: StatsQuery
  compare: Compare
  /** Without a handler the tiles are plain read-only figures. */
  onSelectMetric?: (metric: StatMetric) => void
  /** Show only these tiles; the underlying query is unchanged so the cache is shared. */
  metrics?: readonly StatMetric[]
}

function dateAtUTC(value: string): Date {
  return new Date(`${value}T00:00:00Z`)
}

function dateString(value: Date): string {
  return value.toISOString().slice(0, 10)
}

function shiftDays(value: string, days: number): string {
  const date = dateAtUTC(value)
  date.setUTCDate(date.getUTCDate() + days)
  return dateString(date)
}

function previousYear(value: string): string {
  const date = dateAtUTC(value)
  const month = date.getUTCMonth()
  date.setUTCFullYear(date.getUTCFullYear() - 1)
  if (date.getUTCMonth() !== month) date.setUTCDate(0)
  return dateString(date)
}

function comparisonRange(
  range: StatsQuery['dateRange'],
  compare: Compare,
): StatsQuery['dateRange'] {
  if (compare === 'off') return range
  if (compare === 'previous_year') {
    return { from: previousYear(range.from), to: previousYear(range.to) }
  }
  const days =
    Math.round(
      (dateAtUTC(range.to).getTime() - dateAtUTC(range.from).getTime()) /
        86_400_000,
    ) + 1
  return {
    from: shiftDays(range.from, -days),
    to: shiftDays(range.to, -days),
  }
}

function metricLabel(metric: StatMetric): string {
  return metric
    .split('_')
    .map((word) => word[0]?.toUpperCase() + word.slice(1))
    .join(' ')
}

export function metricValue(metric: StatMetric, value: number): string {
  if (metric === 'engagement_time' && value <= 0) return '—'
  if (metric === 'bounce_rate') return fmt.percent(value)
  if (metric === 'visit_duration' || metric === 'engagement_time') {
    return fmt.duration(value)
  }
  return fmt.number(value)
}

function deltaValue(current: number, previous: number): number | undefined {
  if (previous === 0) return undefined
  return ((current - previous) / Math.abs(previous)) * 100
}

function Delta({ metric, value }: { metric: StatMetric; value?: number }) {
  if (value === undefined || value === 0) {
    return (
      <span className="text-xs text-muted-foreground" data-trend="neutral">
        —
      </span>
    )
  }
  const rising = value > 0
  const good = metric === 'bounce_rate' ? !rising : rising
  return (
    <span
      className={good ? 'text-xs text-accent' : 'text-xs text-destructive'}
      data-trend={good ? 'good' : 'bad'}
    >
      {rising ? '▲' : '▼'} {fmt.percent(Math.abs(value))}
    </span>
  )
}

export function StatsStrip({
  query,
  compare,
  onSelectMetric,
  metrics: shownMetrics,
}: StatsStripProps) {
  const currentQuery: StatsQuery = {
    ...query,
    metrics: [...statMetrics],
    dimensions: undefined,
    compare: undefined,
  }
  const comparisonQuery: StatsQuery = {
    ...currentQuery,
    dateRange: comparisonRange(query.dateRange, compare),
  }
  const current = useStats(currentQuery)
  const comparison = useStats(comparisonQuery)
  const importedIncluded = current.data?.meta.imported_included
  if (current.isError || (compare !== 'off' && comparison.isError)) {
    const failed = current.isError ? current : comparison
    return <ErrorState detail={failed.error?.detail} onRetry={() => void failed.refetch()} />
  }

  return (
    <div className="flex flex-col gap-2">
      {importedIncluded ? (
        <p className="self-start rounded-full border px-2 py-0.5 text-xs text-muted-foreground">
          Includes imported data
        </p>
      ) : null}
      <section
      aria-busy={current.isLoading || comparison.isLoading}
      className="grid grid-cols-2 gap-3 sm:grid-cols-4 xl:grid-cols-7"
    >
      {statMetrics.filter((metric) => !shownMetrics || shownMetrics.includes(metric)).map((metric) => {
        const primary = metric === 'visitors' || metric === 'pageviews'
        const value = current.data?.totals[metric] ?? 0
        const previous = comparison.data?.totals[metric] ?? 0
        const delta = compare === 'off' ? undefined : deltaValue(value, previous)
        const className = 'h-auto min-w-0 flex-col items-start gap-1.5 rounded-xl bg-card px-4 py-3.5 text-left shadow-xs hover:bg-muted/50'
        const content = (
          <>
            <span className="w-full truncate text-xs font-normal text-muted-foreground">
              {metricLabel(metric)}
            </span>
            <span className="text-xl font-semibold leading-none tracking-tight tabular-nums">
              {current.isLoading ? '—' : metricValue(metric, value)}
            </span>
            {compare === 'off' ? null : (
              <Delta metric={metric} value={delta} />
            )}
          </>
        )
        if (!onSelectMetric) {
          return (
            <div
              key={metric}
              data-priority={primary ? 'primary' : 'secondary'}
              className={`${className.replace('hover:bg-muted/50', '')} flex border`}
            >
              {content}
            </div>
          )
        }
        return (
          <Button
            key={metric}
            data-priority={primary ? 'primary' : 'secondary'}
            type="button"
            variant="outline"
            className={className}
            onClick={() => onSelectMetric(metric)}
          >
            {content}
          </Button>
        )
      })}
      </section>
    </div>
  )
}
