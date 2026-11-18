import { ErrorState } from './ErrorState'
import { useEffect, useId, useState, type ReactNode } from 'react'
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceArea,
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
import { intervalAllowed, resolveInterval, trimFutureBuckets, type ChartInterval } from '@/lib/chart'
import { fmt } from '../lib/format'
import type { Compare } from '../lib/search'
import { useTimeseries, type StatsQuery } from '../lib/stats'
import { en } from '../messages/en'
import { activeLocale } from '@/lib/i18n/intl'
import { lastBucketIsPartial, splitSeries } from '@/lib/chart-ranges'
import { usePrefersReducedMotion } from '@/lib/usePrefersReducedMotion'
import { dashboardView } from '@/messages/dashboardView'

export { autoInterval, type ChartInterval } from '@/lib/chart'

type ChartMetric = StatsQuery['metrics'][number]

type MainChartProps = {
  query: StatsQuery
  metric: ChartMetric
  compare: Compare
  /** Requested granularity (URL or remembered); resolved against the range. */
  interval?: ChartInterval
  /** Called when the user picks a granularity; without it the choice is local to the chart. */
  onIntervalChange?: (interval: ChartInterval) => void
  /** Controls shown in the chart card's header, next to the granularity select. */
  headerActions?: ReactNode
  /** The current time; buckets that start after it are not drawn. Tests pass a fixed value. */
  now?: Date
  annotations?: Annotation[]
  canEditAnnotations?: boolean
  onEditAnnotation?: (annotation: Annotation) => void
  onDeleteAnnotation?: (annotation: Annotation) => void
  /** Inside a parent card: no border or background of its own. */
  embedded?: boolean
}

type ChartPoint = {
  time: string
  /** Null for a bucket that has not started yet: the line stops at the current bucket. */
  value: number | null
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

/** A metric that may be null: the API sends null for buckets after now. */
function nullableNumeric(row: Record<string, unknown>, key: string): number | null {
  const value = row[key]
  if (value === null) return null
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
    return new Intl.DateTimeFormat(activeLocale(), {
      timeZone: isSiteLocalBucket ? 'UTC' : timezone,
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    }).format(instant)
  } catch {
    return new Intl.DateTimeFormat(activeLocale(), {
      timeZone: 'UTC',
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    }).format(instant)
  }
}

/** Minimum pixel gap between x-axis labels; wider on narrow charts so labels thin out instead of touching. */
export function xTickGap(width: number): number {
  if (width > 0 && width < 480) return 72
  if (width > 0 && width < 720) return 48
  return 32
}

export function axisBucketLabel(value: string, interval: ChartInterval, timezone: string): string {
  if (interval === 'hour') {
    const isSiteLocalBucket = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$/.test(value)
    const instant = new Date(isSiteLocalBucket ? `${value}Z` : value)
    return new Intl.DateTimeFormat(activeLocale(), {
      timeZone: isSiteLocalBucket ? 'UTC' : timezone,
      hour: 'numeric',
      minute: instant.getUTCMinutes() === 0 ? undefined : '2-digit',
    }).format(instant)
  }
  return new Intl.DateTimeFormat(activeLocale(), {
    timeZone: 'UTC',
    month: 'short',
    day: 'numeric',
  }).format(new Date(`${value.slice(0, 10)}T12:00:00Z`))
}

function chronological(rows: Array<Record<string, unknown>>): Array<Record<string, unknown>> {
  return [...rows].sort((left, right) => timeValue(left).localeCompare(timeValue(right)))
}

type TooltipPayloadItem = { payload?: ChartPoint & { partialBucket?: boolean; noData?: boolean } }

/** Crosshair tooltip: the exact date and value, and a note when the bucket is still filling or precedes tracking. */
function ChartHoverCard({
  active,
  payload,
  metric,
  interval,
  timezone,
  showComparison,
}: {
  active?: boolean
  payload?: TooltipPayloadItem[]
  metric: ChartMetric
  interval: ChartInterval
  timezone: string
  showComparison: boolean
}) {
  const point = payload?.find((item) => item.payload)?.payload
  if (!active || !point) return null
  return (
    <div className="rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md" data-testid="chart-hover-card">
      <p className="mb-1 text-muted-foreground">{timeBucketLabel(point.time, interval, timezone)}</p>
      <p className="text-sm font-medium tabular-nums">
        {point.value === null ? '–' : metricValue(metric, point.value)}
        <span className="ml-1.5 font-normal text-muted-foreground">{metricLabel(metric)}</span>
      </p>
      {point.partialBucket ? <p className="mt-1 text-muted-foreground">{interval === 'hour' || interval === 'day' ? dashboardView.todaySoFar : dashboardView.partialPeriod}</p> : null}
      {point.noData ? <p className="mt-1 text-muted-foreground">{dashboardView.noDataYet}</p> : null}
      {showComparison && point.comparison !== undefined ? (
        <p className="mt-1 tabular-nums text-muted-foreground">{metricValue(metric, point.comparison)} compared</p>
      ) : null}
    </div>
  )
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
  onIntervalChange,
  headerActions,
  now,
  annotations = [],
  canEditAnnotations = false,
  onEditAnnotation,
  onDeleteAnnotation,
  embedded = false,
}: MainChartProps) {
  const summaryId = useId()
  const reducedMotion = usePrefersReducedMotion()
  const [chartWidth, setChartWidth] = useState(0)
  const [localInterval, setLocalInterval] = useState<ChartInterval | undefined>(interval)

  // Without a controlling page (dashboard widgets) a local choice resets when the range changes.
  useEffect(() => {
    setLocalInterval(interval)
  }, [interval, query.dateRange.from, query.dateRange.to])
  const chartMetric: ChartMetric = metric === 'exit_rate' || metric === 'time_on_page'
    ? 'visitors'
    : metric
  const resolvedInterval = resolveInterval(query.dateRange.from, query.dateRange.to, onIntervalChange ? interval : localInterval)
  const chooseInterval = (next: ChartInterval) => {
    if (onIntervalChange) onIntervalChange(next)
    else setLocalInterval(next)
  }
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
  const timezone = query.timezone ?? 'UTC'
  // The chart ends at the current, still-filling bucket: future buckets are not drawn at all.
  // The comparison is matched by position, so it is trimmed to the same length.
  const points: ChartPoint[] = trimFutureBuckets(
    chronological(current.data?.rows ?? []).map((row, index) => ({
      time: timeValue(row),
      value: nullableNumeric(row, chartMetric),
      ...(compare === 'off'
        ? {}
        : { comparison: numeric(comparisonRows[index] ?? {}, chartMetric) }),
    })),
    resolvedInterval,
    now ?? new Date(),
    timezone,
  )
  const lastBucket = points[points.length - 1]?.time
  const partialLast = lastBucketIsPartial(lastBucket, query.dateRange.to, resolvedInterval, now ?? new Date(), timezone)
  const drawn = splitSeries(points, partialLast)
  const noDataCount = drawn.filter((point) => point.noData).length
  const trackingStarted = noDataCount > 0 ? drawn[noDataCount]?.time : undefined
  // Annotations are placed across the drawn days only.
  const drawnRange = lastBucket && lastBucket.slice(0, 10) < query.dateRange.to
    ? { from: query.dateRange.from, to: lastBucket.slice(0, 10) }
    : query.dateRange
  const label = `${metricLabel(chartMetric)} over time`
  const measured = points.filter((point): point is ChartPoint & { value: number } => point.value !== null)
  const peak = measured.reduce<(ChartPoint & { value: number }) | undefined>((best, point) => !best || point.value > best.value ? point : best, undefined)
  const low = measured.reduce<(ChartPoint & { value: number }) | undefined>((best, point) => !best || point.value < best.value ? point : best, undefined)
  const total = numeric(current.data?.totals ?? {}, chartMetric)
  const pointSummary = (point: (ChartPoint & { value: number }) | undefined) => point
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
      data-last-bucket={lastBucket}
      className={embedded ? 'p-4 text-card-foreground' : 'rounded-xl border bg-card p-4 text-card-foreground shadow-xs'}
    >
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <ul aria-label="Chart notes" className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
          <li className="font-medium text-foreground">{dashboardView.kpi.labels[chartMetric] ?? metricLabel(chartMetric)}</li>
          {trackingStarted ? (
            <li className="flex items-center gap-1.5" data-testid="tracking-started">
              <span aria-hidden="true" className="inline-block h-3 w-4 rounded-sm bg-muted ring-1 ring-border" />
              {dashboardView.trackingStarted(fmt.date(trackingStarted, timezone))}
            </li>
          ) : null}
          {partialLast ? (
            <li className="flex items-center gap-1.5" data-testid="partial-note">
              <span aria-hidden="true" className="inline-block w-4 border-t-2 border-dashed border-chart-1" />
              {resolvedInterval === 'hour' || resolvedInterval === 'day' ? dashboardView.todaySoFar : dashboardView.partialPeriod}
            </li>
          ) : null}
        </ul>
        <div className="flex flex-wrap items-center justify-end gap-2">
        {headerActions}
        <Select value={resolvedInterval} onValueChange={(value) => chooseInterval(value as ChartInterval)}>
          <SelectTrigger aria-label="Chart interval" size="sm">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem disabled={!intervalAllowed('hour', query.dateRange.from, query.dateRange.to)} value="hour">Hour</SelectItem>
              <SelectItem value="day">Day</SelectItem>
              <SelectItem value="week">Week</SelectItem>
              <SelectItem value="month">Month</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
        </div>
      </div>
      <p className="sr-only" id={summaryId}>{summary}</p>
      <ChartContainer>
        <AnnotationMarkers
          annotations={annotations}
          range={drawnRange}
          timezone={query.timezone ?? 'UTC'}
          canEdit={canEditAnnotations}
          onEdit={onEditAnnotation}
          onDelete={onDeleteAnnotation}
        />
        <div aria-describedby={summaryId} aria-label={label} className="size-full" role="img">
          <ResponsiveContainer width="100%" height="100%" minWidth={0} onResize={(width) => setChartWidth(width)}>
            <ComposedChart accessibilityLayer data={drawn}>
              <defs>
                <linearGradient id="main-chart-fill" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="var(--chart-1)" stopOpacity={0.25} />
                  <stop offset="95%" stopColor="var(--chart-1)" stopOpacity={0} />
                </linearGradient>
              </defs>
              <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 4" />
              {noDataCount > 0 ? (
                <ReferenceArea
                  x1={drawn[0]!.time}
                  x2={drawn[noDataCount - 1]!.time}
                  fill="var(--muted)"
                  fillOpacity={0.6}
                  ifOverflow="visible"
                  data-testid="no-data-shade"
                />
              ) : null}
              <XAxis dataKey="time" axisLine={false} tick={{ fontSize: 12, fill: 'var(--muted-foreground)' }} tickLine={false} tickMargin={8} interval="preserveStartEnd" minTickGap={xTickGap(chartWidth)} tickFormatter={(value) => axisBucketLabel(String(value), resolvedInterval, timezone)} />
              <YAxis axisLine={false} tick={{ fontSize: 12, fill: 'var(--muted-foreground)' }} tickLine={false} tickMargin={8} width={44} />
              <Tooltip
                cursor={{ stroke: 'var(--muted-foreground)', strokeWidth: 1, strokeDasharray: '3 3' }}
                content={<ChartHoverCard metric={chartMetric} interval={resolvedInterval} timezone={timezone} showComparison={compare !== 'off'} />}
              />
              <Area
                type="linear"
                dataKey="solid"
                fill="url(#main-chart-fill)"
                isAnimationActive={!reducedMotion}
                stroke="var(--chart-1)"
                strokeWidth={2}
                activeDot={{ r: 4, strokeWidth: 2, stroke: 'var(--card)' }}
              />
              {partialLast ? (
                <Line
                  type="linear"
                  dataKey="partial"
                  dot={false}
                  isAnimationActive={!reducedMotion}
                  stroke="var(--chart-1)"
                  strokeDasharray="5 4"
                  strokeWidth={2}
                  activeDot={{ r: 4, strokeWidth: 2, stroke: 'var(--card)' }}
                />
              ) : null}
              {compare === 'off' ? null : (
                <Line
                  type="linear"
                  dataKey="comparison"
                  dot={false}
                  isAnimationActive={!reducedMotion}
                  stroke="var(--chart-2)"
                  strokeDasharray="2 4"
                  strokeOpacity={0.7}
                  strokeWidth={1.5}
                />
              )}
            </ComposedChart>
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
              <td>{point.value === null ? '–' : metricValue(chartMetric, point.value)}</td>
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
