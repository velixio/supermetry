import { ErrorState } from './ErrorState'
import { useEffect, useId, useState, type ReactNode } from 'react'
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
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Tooltip as AnnotationTooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import type { Annotation } from '@/features/annotations/types'
import { autoInterval, type ChartInterval } from '@/lib/chart'
import { fmt } from '../lib/format'
import type { Compare } from '../lib/search'
import { useTimeseries, type StatsQuery } from '../lib/stats'
import { en } from '../messages/en'

export { autoInterval, type ChartInterval } from '@/lib/chart'

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

function timeBucketLabel(value: string, interval: ChartInterval, timezone: string): string {
  if (interval !== 'hour') return fmt.date(value, timezone)
  const isSiteLocalBucket = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/.test(value)
  const instant = new Date(isSiteLocalBucket ? `${value}Z` : value)
  try {
    return new Intl.DateTimeFormat('en-US', {
      timeZone: isSiteLocalBucket ? 'UTC' : timezone,
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    }).format(instant)
  } catch {
    return new Intl.DateTimeFormat('en-US', {
      timeZone: 'UTC',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    }).format(instant)
  }
}

export function axisBucketLabel(value: string, interval: ChartInterval, timezone: string): string {
  if (interval === 'hour') {
    const isSiteLocalBucket = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/.test(value)
    const instant = new Date(isSiteLocalBucket ? `${value}Z` : value)
    return new Intl.DateTimeFormat('en-US', {
      timeZone: isSiteLocalBucket ? 'UTC' : timezone,
      hour: 'numeric',
      minute: instant.getUTCMinutes() === 0 ? undefined : '2-digit',
    }).format(instant)
  }
  return new Intl.DateTimeFormat('en-US', {
    timeZone: 'UTC',
    month: 'short',
    day: 'numeric',
  }).format(new Date(`${value.slice(0, 10)}T12:00:00Z`))
}

function chronological(rows: Array<Record<string, unknown>>): Array<Record<string, unknown>> {
  return [...rows].sort((left, right) => timeValue(left).localeCompare(timeValue(right)))
}

function rangeDays(from: string, to: string): number {
  return Math.floor((dateAtUTC(to).getTime() - dateAtUTC(from).getTime()) / 86_400_000) + 1
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
  const summaryId = useId()
  const [selectedInterval, setSelectedInterval] = useState<ChartInterval | undefined>(interval)

  useEffect(() => {
    setSelectedInterval(interval)
  }, [interval])
  const chartMetric: ChartMetric = metric === 'exit_rate' || metric === 'time_on_page'
    ? 'visitors'
    : metric
  const automaticInterval = autoInterval(query.dateRange.from, query.dateRange.to)
  const requestedInterval = selectedInterval ?? automaticInterval
  const resolvedInterval = requestedInterval === 'hour' && rangeDays(query.dateRange.from, query.dateRange.to) > 7
    ? automaticInterval
    : requestedInterval
  const currentQuery: StatsQuery = {
    ...query,
    metrics: [chartMetric],
    dimensions: undefined,
    compare: compare === 'previous_period' ? compare : undefined,
  }
  const comparisonQuery: StatsQuery = {
    ...currentQuery,
    dateRange: comparisonRange(query.dateRange, compare),
  }
  const current = useTimeseries(currentQuery, resolvedInterval, true)
  const previousYearResult = useTimeseries(comparisonQuery, resolvedInterval, compare === 'previous_year')
  const comparisonRows = chronological(compare === 'previous_period'
    ? current.data?.comparison?.rows ?? []
    : previousYearResult.data?.rows ?? [])
  const points: ChartPoint[] = chronological(current.data?.rows ?? []).map((row, index) => ({
    time: timeValue(row),
    value: numeric(row, chartMetric),
    ...(compare === 'off'
      ? {}
      : { comparison: numeric(comparisonRows[index] ?? {}, chartMetric) }),
  }))
  const label = `${metricLabel(chartMetric)} over time`
  const timezone = query.timezone ?? 'UTC'
  const peak = points.reduce<ChartPoint | undefined>((best, point) => !best || point.value > best.value ? point : best, undefined)
  const low = points.reduce<ChartPoint | undefined>((best, point) => !best || point.value < best.value ? point : best, undefined)
  const total = numeric(current.data?.totals ?? {}, chartMetric)
  const pointSummary = (point: ChartPoint | undefined) => point
    ? en.a11y2.peakOn.replace('{value}', metricValue(chartMetric, point.value)).replace('{time}', timeBucketLabel(point.time, resolvedInterval, timezone))
    : '0'
  const summary = en.a11y2.dataSummary
    .replace('{title}', `${label.charAt(0).toUpperCase()}${label.slice(1)}`)
    .replace('{from}', fmt.date(query.dateRange.from, timezone))
    .replace('{to}', fmt.date(query.dateRange.to, timezone))
    .replace('{total}', metricValue(chartMetric, total))
    .replace('{peak}', pointSummary(peak))
    .replace('{low}', pointSummary(low))
  if (current.isError || (compare === 'previous_year' && previousYearResult.isError)) {
    const failed = current.isError ? current : previousYearResult
    return <ErrorState detail={failed.error?.detail} onRetry={() => void failed.refetch()} />
  }

  return (
    <section
      aria-busy={current.isLoading || previousYearResult.isLoading}
      aria-label={label}
      data-interval={resolvedInterval}
      className="rounded-lg border bg-card p-4 text-card-foreground"
    >
      <div className="mb-3 flex justify-end">
        <Select value={resolvedInterval} onValueChange={(value) => setSelectedInterval(value as ChartInterval)}>
          <SelectTrigger aria-label="Chart interval" size="sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem disabled={rangeDays(query.dateRange.from, query.dateRange.to) > 7} value="hour">Hour</SelectItem>
              <SelectItem value="day">Day</SelectItem>
              <SelectItem value="week">Week</SelectItem>
              <SelectItem value="month">Month</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      </div>
      <p className="sr-only" id={summaryId}>{summary}</p>
      <ChartContainer>
        <AnnotationMarkers
          annotations={annotations}
          range={query.dateRange}
          timezone={query.timezone ?? 'UTC'}
          canEdit={canEditAnnotations}
          onEdit={onEditAnnotation}
          onDelete={onDeleteAnnotation}
        />
        <div aria-describedby={summaryId} aria-label={label} className="size-full" role="img">
          <ResponsiveContainer width="100%" height="100%" minWidth={0}>
            <AreaChart accessibilityLayer data={points}>
              <defs>
                <linearGradient id="main-chart-fill" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--chart-1)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="time" axisLine={false} tick={{ fontSize: 12 }} tickLine={false} interval="preserveStartEnd" minTickGap={32} tickFormatter={(value) => axisBucketLabel(String(value), resolvedInterval, timezone)} />
              <YAxis axisLine={false} tick={{ fontSize: 12 }} tickLine={false} width={40} />
              <Tooltip labelFormatter={(value) => timeBucketLabel(String(value), resolvedInterval, timezone)} />
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
        </div>
      </ChartContainer>

      <table className="sr-only" aria-label={label}>
        <thead>
          <tr>
            <th scope="col">time</th>
            <th scope="col">{metricLabel(chartMetric)}</th>
            {compare === 'off' ? null : <th scope="col">{compare}</th>}
          </tr>
        </thead>
        <tbody>
          {points.map((point) => (
            <tr key={point.time}>
              <th scope="row">{timeBucketLabel(point.time, resolvedInterval, timezone)}</th>
              <td>{metricValue(chartMetric, point.value)}</td>
              {compare === 'off' ? null : (
                <td>{metricValue(chartMetric, point.comparison ?? 0)}</td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  )
}
