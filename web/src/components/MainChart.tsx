import type { ReactNode } from 'react'
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { fmt } from '../lib/format'
import type { Compare } from '../lib/search'
import { useStats, type StatsQuery } from '../lib/stats'

export type ChartInterval = 'hour' | 'day' | 'week' | 'month'

type ChartMetric = StatsQuery['metrics'][number]

type MainChartProps = {
  query: StatsQuery
  metric: ChartMetric
  compare: Compare
  interval?: ChartInterval
}

type ChartPoint = {
  time: string
  value: number
  comparison?: number
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

function metricLabel(metric: ChartMetric): string {
  return metric.split('_').join(' ')
}

function metricValue(metric: ChartMetric, value: number): string {
  if (metric === 'bounce_rate' || metric === 'conversion_rate') {
    return fmt.percent(value)
  }
  if (metric === 'visit_duration' || metric === 'engagement_time') {
    return fmt.duration(value)
  }
  return fmt.number(value)
}

function numeric(row: Record<string, unknown>, key: string): number {
  const value = row[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function timeValue(row: Record<string, unknown>): string {
  return typeof row.time === 'string' ? row.time : ''
}

function ChartContainer({ children }: { children: ReactNode }) {
  return <div className="h-64 min-h-64 w-full">{children}</div>
}

export function autoInterval(from: string, to: string): ChartInterval {
  const days =
    Math.floor(
      (dateAtUTC(to).getTime() - dateAtUTC(from).getTime()) / 86_400_000,
    ) + 1
  if (days <= 2) return 'hour'
  if (days <= 92) return 'day'
  if (days <= 183) return 'week'
  return 'month'
}

export function MainChart({
  query,
  metric,
  compare,
  interval,
}: MainChartProps) {
  const currentQuery: StatsQuery = {
    ...query,
    metrics: [metric],
    dimensions: ['time'],
    compare: undefined,
  }
  const comparisonQuery: StatsQuery = {
    ...currentQuery,
    dateRange: comparisonRange(query.dateRange, compare),
  }
  const current = useStats(currentQuery)
  const comparison = useStats(comparisonQuery)
  const comparisonRows = comparison.data?.rows ?? []
  const points: ChartPoint[] = (current.data?.rows ?? []).map((row, index) => ({
    time: timeValue(row),
    value: numeric(row, metric),
    ...(compare === 'off'
      ? {}
      : { comparison: numeric(comparisonRows[index] ?? {}, metric) }),
  }))
  const resolvedInterval =
    interval ?? autoInterval(query.dateRange.from, query.dateRange.to)
  const label = `${metricLabel(metric)} over time`

  return (
    <section
      aria-busy={current.isLoading || comparison.isLoading}
      aria-label={label}
      data-interval={resolvedInterval}
      className="rounded-lg border bg-card p-4 text-card-foreground"
    >
      <ChartContainer>
        <ResponsiveContainer width="100%" height="100%" minWidth={0}>
          <AreaChart accessibilityLayer data={points}>
            <defs>
              <linearGradient id="main-chart-fill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="var(--chart-1)" stopOpacity={0.25} />
                <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="time" axisLine={false} tickLine={false} />
            <YAxis axisLine={false} tickLine={false} width={40} />
            <Tooltip />
            <Area
              dataKey="value"
              fill="url(#main-chart-fill)"
              stroke="var(--chart-1)"
              strokeWidth={2}
            />
            {compare === 'off' ? null : (
              <Area
                dataKey="comparison"
                fill="none"
                stroke="var(--chart-2)"
                strokeDasharray="6 6"
                strokeOpacity={0.4}
              />
            )}
          </AreaChart>
        </ResponsiveContainer>
      </ChartContainer>

      <table className="sr-only" aria-label={label}>
        <thead>
          <tr>
            <th scope="col">time</th>
            <th scope="col">{metricLabel(metric)}</th>
            {compare === 'off' ? null : <th scope="col">{compare}</th>}
          </tr>
        </thead>
        <tbody>
          {points.map((point) => (
            <tr key={point.time}>
              <th scope="row">{fmt.date(point.time, query.timezone ?? 'UTC')}</th>
              <td>{metricValue(metric, point.value)}</td>
              {compare === 'off' ? null : (
                <td>{metricValue(metric, point.comparison ?? 0)}</td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}
