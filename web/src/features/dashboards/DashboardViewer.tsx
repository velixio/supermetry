import type { CSSProperties } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import type { Compare, Filter, Period } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'
import { en } from '@/messages/en'

import { useDashboards } from './api'
import { readingOrder, widgetQuery } from './layout'
import { WidgetBody, WidgetBoundary, widgetTitle } from './widgets'

const t = en.dashView

export type DashboardViewerProps = {
  dashboardId: number
  base: StatsQuery
  timezone: string
  urlFilters: Filter[]
  /** Period from the URL; the switcher writes the layout's own period there when it opens a dashboard. */
  period?: Period
  from?: string
  to?: string
  compare: Compare
  anonymous: boolean
  onFilter: (filter: Filter) => void
  onShorten: (days: number) => void
  onOpenStandard: () => void
}

/** Renders one saved dashboard's layout with the existing dashboard cards. */
export function DashboardViewer(props: DashboardViewerProps) {
  const dashboards = useDashboards(props.base.siteId)
  if (dashboards.isError) return <ErrorState detail={dashboards.error?.detail ?? t.loadError} onRetry={() => void dashboards.refetch()} />
  if (dashboards.isPending) {
    return (
      <div aria-busy="true" aria-label={t.loading(t.switcherLabel)} className="grid grid-cols-1 gap-4 md:grid-cols-2" role="status">
        <Skeleton className="h-40 rounded-xl md:col-span-2" />
        <Skeleton className="h-64 rounded-xl" />
        <Skeleton className="h-64 rounded-xl" />
      </div>
    )
  }
  const dashboard = dashboards.data.find((item) => item.id === props.dashboardId)
  if (!dashboard) {
    return (
      <section className="flex flex-col items-start gap-3 rounded-xl border bg-card p-5" role="status">
        <h2 className="font-medium">{t.missingTitle}</h2>
        <p className="text-sm text-muted-foreground">{t.missingBody}</p>
        <Button className="min-h-11 sm:min-h-9" type="button" variant="outline" onClick={props.onOpenStandard}>{t.openStandard}</Button>
      </section>
    )
  }
  const layout = dashboard.layout
  if (layout.widgets.length === 0) {
    return (
      <section className="flex flex-col items-start gap-2 rounded-xl border bg-card p-5" role="status">
        <h2 className="font-medium">{t.emptyTitle}</h2>
        <p className="text-sm text-muted-foreground">{t.emptyBody}</p>
      </section>
    )
  }
  const compare: Compare = props.compare !== 'off' ? props.compare : (layout.compare ?? 'off')
  const context = { base: props.base, period: props.period, from: props.from, to: props.to, timezone: props.timezone }
  return (
    <div aria-label={t.widgetGrid} className="grid min-w-0 grid-cols-1 items-start gap-4 md:grid-cols-2 lg:grid-cols-12" data-dashboard-id={dashboard.id} role="group">
      {readingOrder(layout.widgets).map((widget) => {
        const title = widgetTitle(widget)
        const style = { '--span': Math.min(12, Math.max(1, widget.w)), '--wide': widget.w >= 7 ? 2 : 1 } as CSSProperties
        return (
          <div
            className="col-span-1 min-w-0 md:col-[span_var(--wide)] lg:col-[span_var(--span)]"
            data-widget-id={widget.id}
            data-widget-kind={widget.kind}
            key={widget.id}
            style={style}
          >
            <WidgetBoundary title={title}>
              <WidgetBody
                anonymous={props.anonymous}
                compare={compare}
                query={widgetQuery(layout, widget, props.urlFilters, { ...context, compare })}
                widget={widget}
                onFilter={props.onFilter}
                onShorten={props.onShorten}
              />
            </WidgetBoundary>
          </div>
        )
      })}
    </div>
  )
}
