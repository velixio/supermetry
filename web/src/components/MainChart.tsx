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
import { FlagIcon, PencilIcon, Trash2Icon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Tooltip as AnnotationTooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { Annotation } from '@/features/annotations/types'
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
  annotations?: Annotation[]
  canEditAnnotations?: boolean
  onEditAnnotation?: (annotation: Annotation) => void
  onDeleteAnnotation?: (annotation: Annotation) => void
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

function chronological(rows: Array<Record<string, unknown>>): Array<Record<string, unknown>> {
  return [...rows].sort((left, right) => timeValue(left).localeCompare(timeValue(right)))
}

function ChartContainer({ children }: { children: ReactNode }) {
  return <div className="relative h-64 min-h-64 w-full">{children}</div>
}

function AnnotationMarkers({
  annotations,
  range,
  timezone,
  canEdit,
  onEdit,
  onDelete,
}: {
  annotations: Annotation[]
  range: StatsQuery['dateRange']
  timezone: string
  canEdit: boolean
  onEdit?: (annotation: Annotation) => void
  onDelete?: (annotation: Annotation) => void
}) {
  const byDay = annotations.reduce<Map<string, Annotation[]>>((groups, annotation) => {
    const group = groups.get(annotation.day) ?? []
    group.push(annotation)
    groups.set(annotation.day, group)
    return groups
  }, new Map())
  const from = dateAtUTC(range.from).getTime()
  const duration = dateAtUTC(range.to).getTime() - from
  const marked = [...byDay].map(([day, items]) => ({
    day,
    items,
    left: duration === 0
      ? 50
      : Math.max(0, Math.min(100, ((dateAtUTC(day).getTime() - from) / duration) * 100)),
  }))

  if (marked.length === 0) return null

  return (
    <TooltipProvider>
      <div className="pointer-events-none absolute inset-x-10 top-1 z-10 h-8" aria-label="Chart annotations">
        {marked.map(({ day, left, items }) => {
          const label = fmt.date(day, timezone)
          return (
            <AnnotationTooltip key={day}>
              <TooltipTrigger asChild>
                <Button
                  className="pointer-events-auto absolute -translate-x-1/2"
                  style={{ left: `${left}%` }}
                  size="icon-xs"
                  variant="secondary"
                  aria-label={`${items.length} ${items.length === 1 ? 'annotation' : 'annotations'} on ${label}`}
                >
                  <FlagIcon />
                </Button>
              </TooltipTrigger>
              <TooltipContent className="flex max-w-sm flex-col items-stretch gap-2 p-2" side="bottom">
                <p className="font-medium">{label}</p>
                {items.map((annotation) => (
                  <div className="flex items-center gap-2" key={annotation.id}>
                    <span className="min-w-0 flex-1">{annotation.text}</span>
                    {canEdit ? (
                      <span className="flex gap-1">
                        <Button
                          aria-label={`Edit ${annotation.text}`}
                          size="icon-xs"
                          variant="secondary"
                          onClick={() => onEdit?.(annotation)}
                        >
                          <PencilIcon />
                        </Button>
                        <Button
                          aria-label={`Delete ${annotation.text}`}
                          size="icon-xs"
                          variant="destructive"
                          onClick={() => onDelete?.(annotation)}
                        >
                          <Trash2Icon />
                        </Button>
                      </span>
                    ) : null}
                  </div>
                ))}
              </TooltipContent>
            </AnnotationTooltip>
          )
        })}
      </div>
    </TooltipProvider>
  )
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
  annotations = [],
  canEditAnnotations = false,
  onEditAnnotation,
  onDeleteAnnotation,
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
  const comparisonRows = chronological(comparison.data?.rows ?? [])
  const points: ChartPoint[] = chronological(current.data?.rows ?? []).map((row, index) => ({
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
        <AnnotationMarkers
          annotations={annotations}
          range={query.dateRange}
          timezone={query.timezone ?? 'UTC'}
          canEdit={canEditAnnotations}
          onEdit={onEditAnnotation}
          onDelete={onDeleteAnnotation}
        />
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
