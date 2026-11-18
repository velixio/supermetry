import { useMemo } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/skeleton'
import { obsMetrics as t } from '@/messages/obsMetrics'

import { useObsMetricQuery } from './api'
import { MetricChart } from './MetricChart'
import { dateRangeInstants, fitInterval, type ObsMetricQuery, type ObsMetricWidgetQuery } from './metricQuery'

type Props = {
  title: string
  siteId: number
  timezone: string
  /** The dashboard's inclusive local date range for this widget. */
  dateRange: { from: string; to: string }
  query?: ObsMetricWidgetQuery
  chart?: 'line' | 'bar'
}

/** The full query of a dashboard widget: its stored query plus the dashboard's time range, with a step the range allows. */
export function widgetMetricQuery(query: ObsMetricWidgetQuery, dateRange: { from: string; to: string }, timezone: string): ObsMetricQuery {
  const { from, to } = dateRangeInstants(dateRange, timezone)
  return { ...query, from, to, interval: fitInterval(query.interval, Date.parse(to) - Date.parse(from)) }
}

/** An obs_metric dashboard widget: one custom metric as a shadcn line or bar chart. */
export function ObsMetricWidget({ title, siteId, timezone, dateRange, query, chart = 'line' }: Props) {
  const full = useMemo(() => (query && query.name ? widgetMetricQuery(query, dateRange, timezone) : null), [query, dateRange, timezone])
  const result = useObsMetricQuery(siteId, full)
  return (
    <section aria-label={title} className="relative min-w-0 overflow-hidden rounded-xl border bg-card text-card-foreground" role="region">
      <header className="flex min-h-12 items-center border-b px-4">
        <h2 className="truncate text-sm font-medium">{title}</h2>
      </header>
      <div className="p-4">
        {!full ? <p className="text-sm text-muted-foreground">{t.widgetMissing}</p>
          : result.isError ? <ErrorState onRetry={() => void result.refetch()} />
          : !result.data ? <Skeleton aria-busy="true" aria-label={t.loading} className="h-40 w-full" role="status" />
          : result.data.series.every((series) => series.points.length === 0) ? <p className="text-sm text-muted-foreground">{t.noData}</p>
          : <MetricChart chart={chart} className="aspect-auto h-48 w-full" interval={full.interval} name={full.name} series={result.data.series} timezone={timezone} />}
      </div>
    </section>
  )
}
