import { ErrorState } from '@/components/ErrorState'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'
import type { Compare, Filter } from '@/lib/search'
import { useStats, type StatsQuery } from '@/lib/stats'

type RevenueReportProps = {
  siteId: number
  currency: string
  timezone: string
  dateRange: StatsQuery['dateRange']
  filters: Filter[]
  compare: Compare
  includeImported: boolean
}

type BreakdownDimension = 'source' | 'channel' | 'utm_campaign'

function currencyFormatter(currency: string): Intl.NumberFormat {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
}

function numeric(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0
}

function comparisonText(current: number, previous: number, compare: Compare): string | undefined {
  if (compare === 'off') return undefined
  if (previous === 0) return current === 0 ? 'No change vs previous period' : 'New vs previous period'
  const change = ((current - previous) / Math.abs(previous)) * 100
  const sign = change > 0 ? '+' : ''
  const label = compare === 'previous_year' ? 'previous year' : 'previous period'
  return `${sign}${fmt.number(change)}% vs ${label}`
}

function MetricCard({
  label, value, previous, formatter, compare,
}: {
  label: string
  value: number
  previous: number
  formatter: Intl.NumberFormat
  compare: Compare
}) {
  return <Card>
    <CardHeader>
      <CardDescription>{label}</CardDescription>
      <p className="text-2xl font-semibold tabular-nums">{formatter.format(value)}</p>
    </CardHeader>
    {comparisonText(value, previous, compare) ? <CardContent className="text-xs text-muted-foreground">{comparisonText(value, previous, compare)}</CardContent> : null}
  </Card>
}

function Trend({ query, currency }: { query: ReturnType<typeof useStats>; currency: string }) {
  const formatter = currencyFormatter(currency)
  if (query.isError) return <Card aria-label="Revenue over time"><ErrorState detail={query.error?.detail} onRetry={() => void query.refetch()} /></Card>
  const rows = query.data?.rows ?? []
  const comparisonRows = query.data?.comparison?.rows ?? []
  const maximum = Math.max(1, ...rows.map((row) => numeric(row.total_revenue)), ...comparisonRows.map((row) => numeric(row.total_revenue)))
  return <Card aria-busy={query.isPending} aria-label="Revenue over time">
    <CardHeader>
      <CardTitle>Revenue over time</CardTitle>
      <CardDescription>Revenue is grouped in {query.data?.rows.some((row) => String(row.time).includes('T')) ? 'hourly' : 'daily'} buckets using the project timezone.</CardDescription>
    </CardHeader>
    <CardContent>
      <div className="flex h-48 items-end gap-1 overflow-hidden" aria-hidden="true">
        {rows.map((row, index) => <div className="flex h-full min-w-1 flex-1 items-end gap-px" key={String(row.time)}>
          <div className="min-w-0 flex-1 rounded-t bg-primary/75" style={{ height: `${Math.max(2, numeric(row.total_revenue) / maximum * 100)}%` }} />
          {comparisonRows[index] ? <div className="min-w-0 flex-1 rounded-t bg-muted-foreground/30" style={{ height: `${Math.max(2, numeric(comparisonRows[index].total_revenue) / maximum * 100)}%` }} /> : null}
        </div>)}
      </div>
      {comparisonRows.length > 0 ? <p className="mt-2 text-xs text-muted-foreground">Solid bars show the selected range; muted bars show the comparison range.</p> : null}
      <div className="overflow-x-auto">
        <table className="sr-only" aria-label="Revenue over time">
          <thead><tr><th scope="col">Date</th><th scope="col">Revenue</th>{comparisonRows.length > 0 ? <th scope="col">Comparison revenue</th> : null}</tr></thead>
          <tbody>{rows.map((row, index) => <tr key={String(row.time)}><th scope="row">{fmt.date(String(row.time), 'UTC')}</th><td>{formatter.format(numeric(row.total_revenue))}</td>{comparisonRows.length > 0 ? <td>{formatter.format(numeric(comparisonRows[index]?.total_revenue))}</td> : null}</tr>)}</tbody>
        </table>
      </div>
    </CardContent>
  </Card>
}

function Breakdown({
  title, dimension, query, currency,
}: {
  title: string
  dimension: BreakdownDimension
  query: ReturnType<typeof useStats>
  currency: string
}) {
  const formatter = currencyFormatter(currency)
  const attribution = query.data?.meta.event_attribution
  const comparisonAttribution = query.data?.comparison?.event_attribution
  const limited = attribution?.coverage === 'available_only' || comparisonAttribution?.coverage === 'available_only'
  return <Card aria-busy={query.isPending}>
    <CardHeader>
      <CardTitle>{title}</CardTitle>
      <CardDescription>Top 10 by event-time revenue</CardDescription>
    </CardHeader>
    {query.isError ? <ErrorState detail={query.error?.detail} onRetry={() => void query.refetch()} /> : <CardContent className="space-y-3">
      {limited ? <p className="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground" role="status">Available attributed revenue only. Some legacy closed events have no event-time attribution and are excluded from this breakdown.</p> : null}
      <div className="overflow-x-auto">
        <table className="w-full min-w-72 text-sm">
          <thead><tr className="border-b text-left text-muted-foreground"><th className="pb-2 font-medium" scope="col">{title}</th><th className="pb-2 text-right font-medium" scope="col">Revenue</th><th className="pb-2 text-right font-medium" scope="col">Average</th></tr></thead>
          <tbody>{(query.data?.rows ?? []).map((row, index) => {
            const label = typeof row[dimension] === 'string' && row[dimension] ? String(row[dimension]) : 'Unknown'
            return <tr className="border-b last:border-0" key={`${label}-${index}`}><th className="py-2 text-left font-normal" scope="row">{label}</th><td className="py-2 text-right tabular-nums">{formatter.format(numeric(row.total_revenue))}</td><td className="py-2 text-right tabular-nums">{formatter.format(numeric(row.average_revenue))}</td></tr>
          })}</tbody>
        </table>
      </div>
    </CardContent>}
  </Card>
}

export function RevenueReport(props: RevenueReportProps) {
  const base: StatsQuery = {
    siteId: props.siteId,
    trackingMode: 'identified',
    dateRange: props.dateRange,
    metrics: ['total_revenue', 'average_revenue'],
    filters: props.filters,
    compare: props.compare,
    timezone: props.timezone,
    includeImported: props.includeImported,
  }
  const trend = useStats({ ...base, dimensions: ['time'], orderBy: [{ column: 'time', direction: 'asc' }], limit: 400 })
  const source = useStats({ ...base, dimensions: ['source'], orderBy: [{ column: 'total_revenue', direction: 'desc' }], limit: 10 })
  const channel = useStats({ ...base, dimensions: ['channel'], orderBy: [{ column: 'total_revenue', direction: 'desc' }], limit: 10 })
  const campaign = useStats({ ...base, dimensions: ['utm_campaign'], orderBy: [{ column: 'total_revenue', direction: 'desc' }], limit: 10 })
  const formatter = currencyFormatter(props.currency)
  const totals = trend.data?.totals
  const comparison = trend.data?.comparison?.totals
  const trendLimited = trend.data?.meta.event_attribution?.coverage === 'available_only' || trend.data?.comparison?.event_attribution?.coverage === 'available_only'

  if (trend.isPending && !trend.data) {
    return <section className="rounded-xl border bg-card p-5" role="status">Loading revenue report…</section>
  }

  if (trend.isError) {
    return <div className="flex min-w-0 flex-col gap-4">
      <div><h1 className="text-xl font-semibold tracking-tight">Revenue</h1><p className="text-sm text-muted-foreground">All amounts use the project’s permanent {props.currency} currency.</p></div>
      <Card><ErrorState detail={trend.error?.detail} onRetry={() => void trend.refetch()} /></Card>
    </div>
  }

  return <div className="flex min-w-0 flex-col gap-4">
    <div>
      <h1 className="text-xl font-semibold tracking-tight">Revenue</h1>
      <p className="text-sm text-muted-foreground">All amounts use the project’s permanent {props.currency} currency.</p>
    </div>
    {trendLimited ? <p className="rounded-md bg-muted px-3 py-2 text-sm text-muted-foreground" role="status">Available attributed revenue only. The selected attribution filters exclude legacy closed events without event-time attribution from the headline and trend.</p> : null}
    <div className="grid gap-3 sm:grid-cols-2">
      <MetricCard label="Total revenue" value={numeric(totals?.total_revenue)} previous={numeric(comparison?.total_revenue)} formatter={formatter} compare={props.compare} />
      <MetricCard label="Average revenue" value={numeric(totals?.average_revenue)} previous={numeric(comparison?.average_revenue)} formatter={formatter} compare={props.compare} />
    </div>
    <Trend query={trend} currency={props.currency} />
    <div className="grid gap-4 xl:grid-cols-3">
      <Breakdown title="Source" dimension="source" query={source} currency={props.currency} />
      <Breakdown title="Channel" dimension="channel" query={channel} currency={props.currency} />
      <Breakdown title="Campaign" dimension="utm_campaign" query={campaign} currency={props.currency} />
    </div>
  </div>
}
