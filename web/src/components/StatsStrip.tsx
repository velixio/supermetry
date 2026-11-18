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
  /** `tabs` joins the figures into one strip that heads the chart below it (the dashboard); `cards` are separate tiles (widgets). */
  variant?: 'cards' | 'tabs'
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

/** Changes smaller than this (in percent) are noise and are not coloured good or bad. */
export const NEUTRAL_DELTA = 1

/** A rising bounce rate is bad; every other card metric is good when it rises. Changes under 1% are neutral. */
export function deltaTrend(metric: StatMetric, value: number | undefined): 'good' | 'bad' | 'neutral' {
  if (value === undefined || Math.abs(value) < NEUTRAL_DELTA) return 'neutral'
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
        {value === undefined ? null : <MinusIcon aria-hidden="true" className="size-3" />}
        {value === undefined ? '\u2014' : formatDelta(value)}
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

/**
 * The two queries behind the strip: the period itself and the period it is compared with. The delta is always
 * computed: with comparison off the previous period is still requested, but not drawn on the chart. The dashboard
 * summary builds the same queries so both share one cache entry each.
 */
export function statsStripQueries(query: StatsQuery, compare: Compare): {
  currentQuery: StatsQuery
  comparisonQuery: StatsQuery
  reference: 'previous_year' | 'previous_period'
} {
  const currentQuery: StatsQuery = {
    ...query,
    metrics: [...statMetrics],
    dimensions: undefined,
    compare: undefined,
  }
  const reference = compare === 'previous_year' ? 'previous_year' : 'previous_period'
  const comparisonQuery: StatsQuery = {
    ...currentQuery,
    dateRange: comparisonRange(query.dateRange, reference),
  }
  return { currentQuery, comparisonQuery, reference }
}

const primaryMetrics: readonly StatMetric[] = ['visitors', 'visits', 'pageviews', 'bounce_rate', 'visit_duration']
const secondaryMetrics: readonly StatMetric[] = ['views_per_visit', 'engagement_time']

export function StatsStrip({
  query,
  compare,
  selected,
  onSelectMetric,
  metrics: shownMetrics,
  variant = 'cards',
}: StatsStripProps) {
  const tabs = variant === 'tabs'
  const selectedSecondary = selected !== undefined && (secondaryMetrics as readonly string[]).includes(selected)
  const [expanded, setExpanded] = useState(selectedSecondary)
  useEffect(() => {
    if (selectedSecondary) setExpanded(true)
  }, [selectedSecondary])
  const { currentQuery, comparisonQuery, reference } = statsStripQueries(query, compare)
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
    const className = tabs
      ? 'relative flex h-full min-w-0 flex-col items-start gap-1.5 bg-card px-4 py-3.5 text-left text-card-foreground'
      : 'flex h-full min-w-0 flex-col items-start gap-2 rounded-xl border bg-card p-4 text-left text-card-foreground shadow-sm'
    const content = (
      <>
        <span className={`w-full truncate text-muted-foreground ${tabs ? 'text-xs font-medium' : 'text-sm'}`}>{metricLabel(metric)}</span>
        {/* Whitespace text nodes keep the parts apart for assistive-tech text matching; the flex layout does not render them. */}
        {' '}
        <span className="text-2xl font-semibold leading-none tabular-nums">{valueText}</span>
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
        className={tabs
          // The selected tab carries the chart's own line colour on its top edge, tying the figure to the line below.
          ? `${className} outline-none transition-colors before:absolute before:inset-x-0 before:top-0 before:h-0.5 before:bg-transparent hover:bg-muted/40 focus-visible:z-10 focus-visible:ring-[3px] focus-visible:ring-ring/50 focus-visible:ring-inset aria-pressed:bg-muted/40 aria-pressed:before:bg-chart-1`
          : `${className} outline-none transition-colors hover:bg-muted/50 focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-pressed:border-foreground/30 aria-pressed:bg-muted/50`}
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
  const grid = tabs
    // One-pixel gaps over the border colour draw hairline dividers however the tabs wrap.
    ? 'grid grid-cols-2 gap-px bg-border sm:grid-cols-3 lg:grid-cols-5'
    : 'grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5'
  const toggle = more.length > 0 ? (
    <Button
      aria-controls="dashboard-more-metrics"
      aria-expanded={expanded}
      className={tabs ? 'h-7 px-2 text-xs text-muted-foreground' : 'self-start'}
      size="sm"
      type="button"
      variant="ghost"
      onClick={() => setExpanded(!expanded)}
    >
      {expanded ? <ChevronUpIcon data-icon="inline-start" /> : <ChevronDownIcon data-icon="inline-start" />}
      {expanded ? dashboardView.kpi.fewerMetrics : dashboardView.kpi.moreMetrics}
    </Button>
  ) : null
  const moreGrid = more.length > 0
    ? <section className={`${grid} ${tabs ? 'border-t' : ''}`} hidden={!expanded} id="dashboard-more-metrics">{expanded ? more.map(card) : null}</section>
    : null

  if (tabs) {
    return (
      <div aria-label={dashboardView.kpi.groupLabel} className="flex flex-col border-b" role="group">
        <section aria-busy={current.isLoading || comparison.isLoading} className={grid}>
          {shown.map(card)}
        </section>
        {moreGrid}
        <div className="flex flex-wrap items-center justify-between gap-x-3 border-t px-2 py-1">
          {toggle ?? <span />}
          <span className="flex items-center gap-2 px-2 text-xs text-muted-foreground" data-testid="kpi-reference">
            {importedIncluded ? <span className="rounded-full border px-2 py-0.5">{dashboardView.importedChip}</span> : null}
            {dashboardView.kpi.changesVs(referenceText)}
          </span>
        </div>
      </div>
    )
  }

  return (
    <div aria-label={dashboardView.kpi.groupLabel} className="flex flex-col gap-3" role="group">
      {importedIncluded ? (
        <p className="self-start rounded-full border px-2 py-0.5 text-xs text-muted-foreground">{dashboardView.importedChip}</p>
      ) : null}
      <section aria-busy={current.isLoading || comparison.isLoading} className={grid}>
        {shown.map(card)}
      </section>
      {toggle}
      {moreGrid}
    </div>
  )
}
