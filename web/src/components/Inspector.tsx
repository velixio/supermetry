import { lazy, Suspense, useRef, useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { metricValue, statMetrics } from '@/components/StatsStrip'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { hourLabel, orderedWeekdays, type TrafficMetric } from '@/features/map/trafficGrid'
import { breakdownLabel } from '@/lib/breakdown-labels'
import { metricLabel } from '@/lib/breakdown-metrics'
import { countryLabel } from '@/lib/country'
import { fmt } from '@/lib/format'
import { inspectAllowed, inspectCrossDimensions, inspectFilter, type InspectTarget } from '@/lib/inspect'
import type { Filter } from '@/lib/search'
import { useBreakdown, useStats, type StatsQuery } from '@/lib/stats'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

const FullReportDialog = lazy(() => import('./FullReportDialog').then((module) => ({ default: module.FullReportDialog })))

/** QueryRequest.filters allows at most 10 entries (internal/api/openapi.yaml). */
const MAX_FILTERS = 10

export type InspectorProps = {
  target: InspectTarget
  query: StatsQuery
  anonymous: boolean
  trafficMetric: TrafficMetric
  onFilter(filter: Filter): void
}

export function inspectorDimensionLabel(dimension: string): string {
  return ui.inspector.dimensions[dimension] ?? dimension
}

export function inspectorTitle(target: InspectTarget): string {
  if (target.dimension === 'weekday_hour') {
    const weekday = orderedWeekdays().find((day) => day.index === Number(target.values[0]))?.name ?? target.values[0]
    return `${weekday} ${hourLabel(Number(target.values[1]))}`
  }
  const [value, country] = target.values
  const label = breakdownLabel(target.dimension, value)
  return country === undefined ? label : `${label}, ${countryLabel(country)}`
}

function sameFilter(left: StatsQuery['filters'][number], right: Filter): boolean {
  return left.dimension === right.dimension && left.operator === right.operator &&
    left.values.length === right.values.length && left.values.every((value, index) => value === right.values[index])
}

export function Inspector(props: InspectorProps) {
  // Defence in depth: the route already filters, but a blocked target must never reach a query.
  if (!inspectAllowed(props.target, props.anonymous)) return null
  if (props.target.dimension === 'weekday_hour') return <TrafficCellBody {...props} />
  const filter = inspectFilter(props.target)
  if (!filter) return null
  const applied = props.query.filters.some((current) => sameFilter(current, filter))
  if (!applied && props.query.filters.length >= MAX_FILTERS) {
    return <p className="p-4 text-sm text-muted-foreground" role="status">{ui.inspector.tooManyFilters}</p>
  }
  return <ValueBody {...props} applied={applied} filter={filter} />
}

function InspectorLoading() {
  return (
    <div aria-label={ui.inspector.loading} className="flex flex-col gap-3 p-4" role="status">
      {Array.from({ length: 4 }, (_, index) => <Skeleton key={index} className="h-8 w-full" />)}
    </div>
  )
}

function OutOfRange() {
  return (
    <div className="flex flex-col gap-1 p-4" role="status">
      <p className="text-sm font-medium">{ui.inspector.outOfRange}</p>
      <p className="text-sm text-muted-foreground">{ui.inspector.outOfRangeDetail}</p>
    </div>
  )
}

function ValueBody({ target, query, anonymous, onFilter, applied, filter }: InspectorProps & { applied: boolean; filter: Filter }) {
  const reportRef = useRef<HTMLButtonElement>(null)
  const [reportOpen, setReportOpen] = useState(false)
  const scoped: StatsQuery = { ...query, filters: applied ? query.filters : [...query.filters, filter] }
  // The request StatsStrip sends once this value is a filter, so the numbers match the dashboard after "Add as filter".
  const totals = useStats({ ...scoped, metrics: [...statMetrics], dimensions: undefined, compare: undefined })
  const reportMetric = target.dimension === 'goal' ? 'conversions' : target.dimension === 'event' ? 'events' : 'visitors'
  const reportQuery: StatsQuery = {
    ...query,
    metrics: [reportMetric],
    dimensions: target.dimension === 'region' || target.dimension === 'city' ? [target.dimension, 'country'] : [target.dimension],
    orderBy: [{ column: reportMetric, direction: 'desc' }],
    limit: 10,
  }

  if (totals.isError) return <ErrorState detail={totals.error?.detail} onRetry={() => void totals.refetch()} />
  if (totals.isPending) return <InspectorLoading />
  const values = totals.data?.totals ?? {}
  if (!(Number(values.visitors ?? 0) > 0)) return <OutOfRange />

  return (
    <>
      <section aria-label={ui.inspector.metrics} className="grid grid-cols-2 gap-2 p-4">
        {statMetrics.map((metric) => (
          <div key={metric} className="rounded-lg bg-muted/50 p-3">
            <p className="text-xs text-muted-foreground">{metricLabel(metric)}</p>
            <p className="mt-1 font-medium tabular-nums">{metricValue(metric, Number(values[metric] ?? 0))}</p>
          </div>
        ))}
      </section>
      <div className="flex flex-col gap-4 border-t p-4 empty:hidden">
        {inspectCrossDimensions(target, anonymous).map((dimension) => <CrossBreakdown key={dimension} dimension={dimension} query={scoped} />)}
      </div>
      <div className="flex flex-wrap items-center gap-2 border-t p-4">
        {applied
          ? <p className="text-sm text-muted-foreground">{ui.inspector.filterApplied}</p>
          : <Button size="sm" type="button" onClick={() => onFilter(filter)}>{ui.inspector.addFilter}</Button>}
        <Button ref={reportRef} aria-haspopup="dialog" size="sm" type="button" variant="outline" onClick={() => setReportOpen(true)}>
          {en.dashboard.viewFullReport}
        </Button>
      </div>
      {reportOpen ? (
        <Suspense fallback={null}>
          <FullReportDialog
            allowSearch={!anonymous}
            dimension={target.dimension}
            query={reportQuery}
            returnFocusRef={reportRef}
            title={inspectorDimensionLabel(target.dimension)}
            onClose={() => setReportOpen(false)}
          />
        </Suspense>
      ) : null}
    </>
  )
}

function CrossBreakdown({ dimension, query }: { dimension: string; query: StatsQuery }) {
  const result = useBreakdown({ ...query, metrics: ['visitors'], dimensions: [dimension], limit: 5, compare: undefined })
  const title = ui.inspector.topBy(inspectorDimensionLabel(dimension))
  if (result.isError) return <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
  if (result.isPending) return <Skeleton aria-label={title} className="h-24 w-full" role="status" />
  const rows = result.data?.rows ?? []
  if (rows.length === 0) return null
  return (
    <section aria-label={title}>
      <h3 className="text-sm font-medium">{title}</h3>
      <ol className="mt-2 flex flex-col gap-1">
        {rows.map((row, index) => (
          <li key={`${String(row[dimension] ?? '')}:${index}`} className="flex min-w-0 items-center justify-between gap-2 text-sm">
            <span className="min-w-0 break-words text-muted-foreground">{breakdownLabel(dimension, String(row[dimension] ?? ''))}</span>
            <span className="tabular-nums">{fmt.number(Number(row.visitors ?? 0))}</span>
          </li>
        ))}
      </ol>
    </section>
  )
}

function TrafficCellBody({ target, query, trafficMetric }: InspectorProps) {
  // Identical to WeeklyRhythmCard's request, so it reads the cached grid instead of fetching again.
  const traffic = useBreakdown({ ...query, metrics: [trafficMetric], dimensions: ['weekday', 'hour_of_day'], limit: 168 })
  if (traffic.isError) return <ErrorState detail={traffic.error?.detail} onRetry={() => void traffic.refetch()} />
  if (traffic.isPending) return <InspectorLoading />
  const row = (traffic.data?.rows ?? []).find((candidate) =>
    Number(candidate.weekday) === Number(target.values[0]) && Number(candidate.hour_of_day) === Number(target.values[1]))
  const value = Number(row?.[trafficMetric] ?? 0)
  if (!row || !(value > 0)) return <OutOfRange />
  return (
    <div className="flex flex-col gap-3 p-4">
      <div className="rounded-lg bg-muted/50 p-3">
        <p className="text-xs text-muted-foreground">{metricLabel(trafficMetric)}</p>
        <p className="mt-1 font-medium tabular-nums">{fmt.number(value)}</p>
      </div>
      <p className="text-sm text-muted-foreground">{ui.inspector.trafficScope}</p>
    </div>
  )
}
