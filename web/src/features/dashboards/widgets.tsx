import { Component, type ErrorInfo, type ReactNode } from 'react'

import { BreakdownCard } from '@/components/BreakdownCard'
import { ErrorState } from '@/components/ErrorState'
import { MainChart } from '@/components/MainChart'
import { isStatMetric, StatsStrip, type StatMetric } from '@/components/StatsStrip'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { FunnelChart } from '@/features/funnels/FunnelChart'
import { useFunnelResult, useFunnels } from '@/features/funnels/api'
import { useGoals } from '@/features/goals/api'
import { WeeklyRhythmCard } from '@/features/map/WeeklyRhythmCard'
import { WhereCard } from '@/features/map/WhereCard'
import { RetentionHeatmap } from '@/features/retention/RetentionHeatmap'
import { useRetentionReport } from '@/features/retention/api'
import { useVitals, type VitalMetricName } from '@/features/vitals/api'
import { fmt } from '@/lib/format'
import type { Compare, Filter } from '@/lib/search'
import { useStats, type StatsQuery } from '@/lib/stats'
import { isAnonymousDimensionAllowed } from '@/lib/tracking-policy'
import { en } from '@/messages/en'

import type { DashboardWidget } from './api'

const t = en.dashView

export type WidgetContext = {
  widget: DashboardWidget
  query: StatsQuery
  compare: Compare
  anonymous: boolean
  onFilter: (filter: Filter) => void
  onShorten: (days: number) => void
}

const kindTitles: Record<DashboardWidget['kind'], string> = {
  kpi_strip: 'Key figures',
  time_series: 'Trend',
  breakdown: 'Breakdown',
  map: 'Where',
  weekly_rhythm: 'Weekly rhythm',
  goals: t.goals,
  funnel: t.funnel,
  retention: t.retention,
  web_vitals: t.vitals,
}

export function widgetTitle(widget: DashboardWidget): string {
  if (widget.title) return widget.title
  if (widget.kind === 'breakdown') return dimensionLabel(widget.config?.dimension ?? '')
  return kindTitles[widget.kind]
}

function dimensionLabel(dimension: string): string {
  if (dimension.startsWith('prop:')) return dimension.slice(5)
  const words = dimension.replace(/_/g, ' ')
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function trafficMetric(widget: DashboardWidget): 'visitors' | 'pageviews' {
  return widget.config?.metric === 'pageviews' ? 'pageviews' : 'visitors'
}

/** Titled card used by the widgets that have no card of their own. */
function WidgetCard({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section aria-label={title} className="relative min-w-0 overflow-hidden rounded-xl border bg-card text-card-foreground" role="region">
      <header className="flex min-h-12 items-center border-b px-4">
        <h2 className="truncate text-sm font-medium">{title}</h2>
      </header>
      <div className="p-4">{children}</div>
    </section>
  )
}

function Loading({ title }: { title: string }) {
  return (
    <div aria-busy="true" aria-label={t.loading(title)} className="flex flex-col gap-3" role="status">
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-4/5" />
      <Skeleton className="h-8 w-3/5" />
    </div>
  )
}

function Message({ children }: { children: ReactNode }) {
  return <p className="text-sm text-muted-foreground">{children}</p>
}

function Restricted({ title }: { title: string }) {
  return (
    <WidgetCard title={title}>
      <Message>Unavailable in anonymous mode because its labels could identify visitors.</Message>
    </WidgetCard>
  )
}

export function WidgetBody(props: WidgetContext) {
  const { widget, query, compare, anonymous } = props
  const title = widgetTitle(widget)
  const config = widget.config ?? {}
  switch (widget.kind) {
    case 'kpi_strip': {
      const shown = (config.metrics ?? []).filter(isStatMetric) as StatMetric[]
      return (
        <div className="flex flex-col gap-2">
          <h2 className="sr-only">{title}</h2>
          <StatsStrip query={query} compare={compare} metrics={shown.length > 0 ? shown : undefined} />
        </div>
      )
    }
    case 'time_series':
      return (
        <div className="flex flex-col gap-2">
          <h2 className="sr-only">{title}</h2>
          <MainChart query={query} metric={config.metric ?? 'visitors'} compare={compare} />
        </div>
      )
    case 'breakdown': {
      const dimension = config.dimension ?? 'page'
      if (anonymous && !isAnonymousDimensionAllowed(dimension)) return <Restricted title={title} />
      const metrics = config.metrics?.length ? config.metrics : (['visitors'] as StatsQuery['metrics'])
      return (
        <BreakdownCard
          title={title}
          query={query}
          tabs={[{ key: dimension, label: title, dimension, metric: metrics[0], metrics }]}
          onFilter={props.onFilter}
          allowSearch={!anonymous}
        />
      )
    }
    case 'map':
      return <WhereCard query={query} metric={trafficMetric(widget)} onFilter={props.onFilter} />
    case 'weekly_rhythm':
      return <WeeklyRhythmCard query={query} metric={trafficMetric(widget)} onShorten={props.onShorten} />
    case 'goals':
      return anonymous ? <Restricted title={title} /> : <GoalsWidget title={title} query={query} limit={config.limit ?? 10} />
    case 'funnel':
      return anonymous ? <Restricted title={title} /> : <FunnelWidget title={title} query={query} funnelId={config.funnel_id} />
    case 'retention':
      return anonymous ? <Restricted title={title} /> : <RetentionWidget title={title} query={query} />
    case 'web_vitals':
      return <VitalsWidget title={title} query={query} />
  }
}

function GoalsWidget({ title, query, limit }: { title: string; query: StatsQuery; limit: number }) {
  const goals = useGoals(query.siteId)
  const conversions = useStats({
    siteId: query.siteId,
    dateRange: query.dateRange,
    metrics: ['conversions', 'visits'] as StatsQuery['metrics'],
    dimensions: ['goal'],
    filters: query.filters,
    timezone: query.timezone,
  })
  if (goals.isError) return <WidgetCard title={title}><ErrorState detail={goals.error?.detail} onRetry={() => void goals.refetch()} /></WidgetCard>
  if (goals.isPending) return <WidgetCard title={title}><Loading title={title} /></WidgetCard>
  if (goals.data.length === 0) return <WidgetCard title={title}><Message>{t.goalsEmpty}</Message></WidgetCard>
  const counts = new Map((conversions.data?.rows ?? []).map((row) => [String(row.goal ?? ''), Number(row.conversions)]))
  const visits = Number(conversions.data?.totals?.visits ?? 0)
  const rows = goals.data
    .map((goal) => ({ goal, count: counts.get(String(goal.id)) ?? 0 }))
    .sort((a, b) => b.count - a.count)
    .slice(0, limit)
  return (
    <WidgetCard title={title}>
      {conversions.isError ? <ErrorState detail={conversions.error?.detail} onRetry={() => void conversions.refetch()} /> : (
        <div className="overflow-x-auto">
          <table aria-busy={conversions.isPending} className="w-full text-sm">
            <thead className="text-left text-muted-foreground">
              <tr><th className="py-2 pr-3 font-medium">{t.goalName}</th><th className="px-3 py-2 text-right font-medium">{t.goalConversions}</th><th className="py-2 pl-3 text-right font-medium">{t.goalRate}</th></tr>
            </thead>
            <tbody>
              {rows.map(({ goal, count }) => (
                <tr className="border-t" key={goal.id}>
                  <th className="max-w-48 truncate py-2 pr-3 text-left font-medium" title={goal.name}>{goal.name}</th>
                  <td className="px-3 py-2 text-right tabular-nums">{conversions.isPending ? '—' : fmt.number(count)}</td>
                  <td className="py-2 pl-3 text-right tabular-nums">{conversions.isPending ? '—' : fmt.percent(visits > 0 ? (count / visits) * 100 : 0)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </WidgetCard>
  )
}

function FunnelWidget({ title, query, funnelId }: { title: string; query: StatsQuery; funnelId?: number }) {
  const funnels = useFunnels(query.siteId)
  const funnel = funnels.data?.find((item) => item.id === funnelId)
  const steps = funnel?.steps ?? []
  const result = useFunnelResult(query.siteId, query.dateRange, steps, 'sessions', 'session')
  if (funnels.isError) return <WidgetCard title={title}><ErrorState detail={funnels.error?.detail} onRetry={() => void funnels.refetch()} /></WidgetCard>
  if (funnels.isPending) return <WidgetCard title={title}><Loading title={title} /></WidgetCard>
  if (!funnel) return <WidgetCard title={title}><Message>{t.funnelMissing}</Message></WidgetCard>
  if (steps.length < 2) return <WidgetCard title={title}><Message>{t.funnelShort}</Message></WidgetCard>
  return (
    <WidgetCard title={title}>
      {result.isError ? <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
        : result.data ? (
          <FunnelChart
            countLabel={result.data.count_by === 'visitors' ? 'Visitors' : 'Sessions'}
            steps={result.data.steps.map((step, index) => ({ label: steps[index]?.value ?? `Step ${step.step}`, count: step.count }))}
          />
        ) : <Loading title={title} />}
    </WidgetCard>
  )
}

function RetentionWidget({ title, query }: { title: string; query: StatsQuery }) {
  const report = useRetentionReport(query.siteId, query.dateRange, query.filters, 'week', 'anonymous')
  return (
    <WidgetCard title={title}>
      {report.isError ? <ErrorState detail={report.error?.detail} onRetry={() => void report.refetch()} />
        : report.isPending ? <Loading title={title} />
        : report.data.cohorts.length === 0 ? <Message>{t.retentionEmpty}</Message>
        : <div className="overflow-x-auto"><RetentionHeatmap cohorts={report.data.cohorts} identityWindow={report.data.identity_window} interval="week" retentionMode={report.data.retention_mode} /></div>}
    </WidgetCard>
  )
}

const vitalDefinitions: Array<{ key: VitalMetricName; name: string }> = [
  { key: 'lcp', name: 'Largest Contentful Paint' },
  { key: 'cls', name: 'Cumulative Layout Shift' },
  { key: 'inp', name: 'Interaction to Next Paint' },
  { key: 'fcp', name: 'First Contentful Paint' },
  { key: 'ttfb', name: 'Time to First Byte' },
]

function formatP75(metric: VitalMetricName, value: number): string {
  if (metric === 'cls') return (value / 1000).toFixed(3)
  if (value >= 1000) return `${(value / 1000).toFixed(1)} s`
  return `${Math.round(value)} ms`
}

function VitalsWidget({ title, query }: { title: string; query: StatsQuery }) {
  const result = useVitals({ siteId: query.siteId, dateRange: query.dateRange, filters: query.filters, dimension: 'page' })
  if (result.isError) return <WidgetCard title={title}><ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} /></WidgetCard>
  if (result.isPending) return <WidgetCard title={title}><Loading title={title} /></WidgetCard>
  const overall = result.data.overall
  const samples = vitalDefinitions.reduce((sum, item) => sum + overall[item.key].samples, 0)
  if (samples === 0) return <WidgetCard title={title}><Message>{t.vitalsEmpty}</Message></WidgetCard>
  return (
    <WidgetCard title={title}>
      <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {vitalDefinitions.map(({ key, name }) => {
          const value = overall[key]
          const good = Math.round(value.good * 100)
          const needs = Math.round(value.needs_improvement * 100)
          const poor = Math.round(value.poor * 100)
          return (
            <div className="min-w-0 rounded-lg border px-3 py-2" key={key}>
              <dt className="truncate text-xs text-muted-foreground">{name}</dt>
              <dd className="text-lg font-semibold tabular-nums">{value.samples > 0 && value.p75 !== undefined ? formatP75(key, value.p75) : '—'}</dd>
              <dd className="text-xs text-muted-foreground">{t.vitalsShares(good, needs, poor)} · {t.vitalsSamples(value.samples)}</dd>
            </div>
          )
        })}
      </dl>
    </WidgetCard>
  )
}

/** Keeps one widget's render failure from taking the whole dashboard down. */
export class WidgetBoundary extends Component<{ title: string; children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('dashboard widget failed', error, info.componentStack)
  }
  render() {
    if (!this.state.failed) return this.props.children
    return (
      <div className="flex min-h-24 flex-col items-start gap-2 rounded-xl border bg-card p-4" role="alert">
        <p className="text-sm text-muted-foreground">{t.widgetFailed(this.props.title)}</p>
        <Button className="min-h-11 sm:min-h-9" size="sm" type="button" variant="outline" onClick={() => this.setState({ failed: false })}>{t.retry}</Button>
      </div>
    )
  }
}
