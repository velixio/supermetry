import { useEffect, useState } from 'react'
import { ArrowDownIcon, ArrowUpIcon, ChevronDownIcon, ChevronUpIcon, MinusIcon } from 'lucide-react'

import { ErrorState } from './ErrorState'
import { Button } from '@/components/ui/button'
import { rangeLengthDays } from '@/lib/chart'
import { dashboardView } from '@/messages/dashboardView'

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
  /** The metric the chart shows; its card is marked as pressed. */
  selected?: string
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
  return dashboardView.kpi.labels[metric] ?? metric
}

export function metricValue(metric: StatMetric, value: number): string {
  if (metric === 'engagement_time' && value <= 0) return '—'
  if (metric === 'bounce_rate') return fmt.percent(value)
  if (metric === 'visit_duration' || metric === 'engagement_time') {
    return fmt.duration(value)
  }
  return fmt.number(value)
}

/** Change from the previous value as a percentage; undefined when there is nothing to compare against. */
export function deltaValue(current: number, previous: number): number | undefined {
  if (!Number.isFinite(current) || !Number.isFinite(previous) || previous === 0) return undefined
  return ((current - previous) / Math.abs(previous)) * 100
}

/** Signed percentage for a delta: "+12%", "−4%", "0%". Uses a true minus sign. */
export function formatDelta(value: number): string {
  const text = fmt.percent(Math.abs(value))
  if (value > 0) return `+${text}`
  if (value < 0) return `\u2212${text}`
  return text
}

/** A rising bounce rate is bad; every other card metric is good when it rises. */
export function deltaTrend(metric: StatMetric, value: number | undefined): 'good' | 'bad' | 'neutral' {
  if (value === undefined || value === 0) return 'neutral'
  return (metric === 'bounce_rate' ? value < 0 : value > 0) ? 'good' : 'bad'
}

/** The wording after "vs": the previous period of the same length, or the same dates a year earlier. */
export function referenceLabel(range: StatsQuery['dateRange'], compare: Compare): string {
  if (compare === 'previous_year') return dashboardView.kpi.vsPreviousYear
  return dashboardView.kpi.vsPrevious(rangeLengthDays(range.from, range.to))
}

function Delta({ metric, value, reference }: { metric: StatMetric; value?: number; reference: string }) {
  const trend = deltaTrend(metric, value)
  if (value === undefined || trend === 'neutral') {
    return (
      <span className="flex items-center gap-1 text-xs text-muted-foreground tabular-nums" data-trend="neutral" title={reference}>
        {value === 0 ? <MinusIcon aria-hidden="true" className="size-3" /> : null}
        {value === 0 ? formatDelta(0) : '\u2014'}
      </span>
    )
  }
  const Arrow = value > 0 ? ArrowUpIcon : ArrowDownIcon
  return (
    <span
      className={`flex items-center gap-1 text-xs font-medium tabular-nums ${trend === 'good' ? 'text-success' : 'text-destructive'}`}
      data-trend={trend}
      title={reference}
    >
      <Arrow aria-hidden="true" className="size-3" />
      {formatDelta(value)}
    </span>
  )
}

/**
 * The spoken form of a card: label, value and, when there is something to compare, direction and size of the change.
 * It starts with the exact text the card shows (label, value, signed change), so the accessible name contains the
 * visible text (WCAG 2.5.3, axe label-content-name-mismatch).
 */
export function cardAriaLabel(metric: StatMetric, valueText: string, delta: number | undefined, reference: string): string {
  const t = dashboardView.kpi
  const label = metricLabel(metric)
  if (delta === undefined) return t.ariaFlat(label, `${valueText} \u2014`, t.deltaNone)
  if (delta === 0) return t.ariaFlat(label, `${valueText} ${formatDelta(0)}`, `${t.deltaUnchanged} ${reference}`)
  return t.ariaDelta(label, `${valueText} ${formatDelta(delta)}`, delta > 0 ? t.up : t.down, fmt.percent(Math.abs(delta)), reference)
}

const primaryMetrics: readonly StatMetric[] = ['visitors', 'visits', 'pageviews', 'bounce_rate', 'visit_duration']
const secondaryMetrics: readonly StatMetric[] = ['views_per_visit', 'engagement_time']

export function StatsStrip({
  query,
  compare,
  selected,
  onSelectMetric,
  metrics: shownMetrics,
}: StatsStripProps) {
  const selectedSecondary = selected !== undefined && (secondaryMetrics as readonly string[]).includes(selected)
  const [expanded, setExpanded] = useState(selectedSecondary)
  useEffect(() => {
    if (selectedSecondary) setExpanded(true)
  }, [selectedSecondary])
  const currentQuery: StatsQuery = {
    ...query,
    metrics: [...statMetrics],
    dimensions: undefined,
    compare: undefined,
  }
  // The delta is always computed: with comparison off the previous period is still requested, but not drawn on the chart.
  const reference = compare === 'previous_year' ? 'previous_year' : 'previous_period'
  const comparisonQuery: StatsQuery = {
    ...currentQuery,
    dateRange: comparisonRange(query.dateRange, reference),
  }
  const current = useStats(currentQuery)
  const comparison = useStats(comparisonQuery)
  const importedIncluded = current.data?.meta.imported_included
  if (current.isError) {
    return <ErrorState detail={current.error?.detail} onRetry={() => void current.refetch()} />
  }
  const referenceText = referenceLabel(query.dateRange, reference)

  const card = (metric: StatMetric) => {
    const primary = primaryMetrics.includes(metric)
    const value = current.data?.totals[metric] ?? 0
    const valueText = current.isLoading ? '\u2014' : metricValue(metric, value)
    const delta = comparison.data && current.data ? deltaValue(value, comparison.data.totals[metric] ?? 0) : undefined
    const isSelected = selected === metric
    const className = 'flex h-auto min-w-0 flex-col items-start gap-1.5 rounded-xl border bg-card px-4 py-3.5 text-left shadow-xs'
    const content = (
      <>
        <span className="w-full truncate text-xs font-normal text-muted-foreground">{metricLabel(metric)}</span>
        {/* Whitespace text nodes keep the parts apart for assistive-tech text matching; the flex layout does not render them. */}
        {' '}
        <span className="text-xl font-semibold leading-none tracking-tight tabular-nums">{valueText}</span>
        {' '}
        {comparison.isLoading ? <span className="h-4" aria-hidden="true" /> : <Delta metric={metric} value={delta} reference={referenceText} />}
      </>
    )
    if (!onSelectMetric) {
      return <div key={metric} className={className} data-priority={primary ? 'primary' : 'secondary'}>{content}</div>
    }
    return (
      <button
        key={metric}
        aria-label={cardAriaLabel(metric, valueText, delta, referenceText)}
        aria-pressed={isSelected}
        className={`${className} outline-none transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring aria-pressed:border-selected-edge aria-pressed:bg-muted/40 aria-pressed:ring-1 aria-pressed:ring-selected-edge`}
        data-priority={primary ? 'primary' : 'secondary'}
        type="button"
        onClick={() => onSelectMetric(metric)}
      >
        {content}
      </button>
    )
  }

  const visible = (list: readonly StatMetric[]) => list.filter((metric) => !shownMetrics || shownMetrics.includes(metric))
  // Without a metric filter (widgets pass one) the strip is the dashboard's own: five primary cards, the rest on demand.
  const shown = shownMetrics ? visible(statMetrics) : visible(primaryMetrics)
  const more = shownMetrics ? [] : visible(secondaryMetrics)
  const grid = 'grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5'

  return (
    <div aria-label={dashboardView.kpi.groupLabel} className="flex flex-col gap-3" role="group">
      {importedIncluded ? (
        <p className="self-start rounded-full border px-2 py-0.5 text-xs text-muted-foreground">{dashboardView.importedChip}</p>
      ) : null}
      <section aria-busy={current.isLoading || comparison.isLoading} className={grid}>
        {shown.map(card)}
      </section>
      {more.length > 0 ? (
        <>
          <Button
            aria-controls="dashboard-more-metrics"
            aria-expanded={expanded}
            className="self-start"
            size="sm"
            type="button"
            variant="ghost"
            onClick={() => setExpanded(!expanded)}
          >
            {expanded ? <ChevronUpIcon data-icon="inline-start" /> : <ChevronDownIcon data-icon="inline-start" />}
            {expanded ? dashboardView.kpi.fewerMetrics : dashboardView.kpi.moreMetrics}
          </Button>
          <section className={grid} hidden={!expanded} id="dashboard-more-metrics">{expanded ? more.map(card) : null}</section>
        </>
      ) : null}
    </div>
  )
}
