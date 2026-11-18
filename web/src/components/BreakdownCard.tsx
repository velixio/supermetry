import { lazy, Suspense, useId, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { dashboardView } from '@/messages/dashboardView'
import { Skeleton } from '@/components/ui/skeleton'
import { countryLabel } from '@/lib/country'
import { breakdownLabel } from '@/lib/breakdown-labels'
import { inspectTargetFor, sameInspect, type InspectControl } from '@/lib/inspect'
import { InspectButton } from './InspectButton'
import { formatBreakdownMetric, metricLabel } from '@/lib/breakdown-metrics'
import { fmt } from '@/lib/format'
import type { Filter } from '@/lib/search'
import { useStats, type StatsQuery } from '@/lib/stats'

import { SegmentedControl } from './SegmentedControl'
import { barWidth, OpenLinkAction, primaryHost, RowLabel, rowLink, SourceIcon, TopListActions, TopListBar, topListRowClass } from './TopList'
import { EmptyState } from './EmptyState'
import { ErrorState } from './ErrorState'
import { Flag } from './Flag'
import { ui } from '@/messages/ui'

const FullReportDialog = lazy(() => import('./FullReportDialog').then((module) => ({ default: module.FullReportDialog })))

export type BreakdownTab = {
  key: string
  label?: string
  dimension: string
  metric: StatsQuery['metrics'][number]
  metrics?: StatsQuery['metrics']
}

type BreakdownCardProps = {
  title: string
  tabs: BreakdownTab[]
  query: StatsQuery
  onFilter: (filter: Filter) => void
  allowSearch?: boolean
  allowFilter?: boolean
  verifiedPaths?: boolean
  description?: string
  inspect?: InspectControl
  /** Hosts the site collects for; the first concrete one turns page paths into links. */
  allowedHosts?: string[]
}

function filterAccessibleLabel(dimension: string, label: string, display: string, country: string): string {
  if (dimension === 'country') return display
  if (dimension === 'region' || dimension === 'city') return `${label}, ${countryLabel(country)}`
  return label
}

export function BreakdownCard({ title, tabs, query, onFilter, allowSearch = true, allowFilter = true, verifiedPaths = false, description, inspect, allowedHosts }: BreakdownCardProps) {
  const filterDescriptionId = useId()
  const panelId = useId()
  const siteHost = primaryHost(allowedHosts)
  const fullReportRef = useRef<HTMLButtonElement>(null)
  const [fullReportOpen, setFullReportOpen] = useState(false)
  const [activeKey, setActiveKey] = useState(tabs[0]?.key ?? '')
  const activeTab = tabs.find((tab) => tab.key === activeKey) ?? tabs[0]
  const cardQuery: StatsQuery = {
    ...query,
    metrics: activeTab ? (activeTab.metrics ?? [activeTab.metric]) : query.metrics,
    dimensions: activeTab
      ? activeTab.dimension === 'region' || activeTab.dimension === 'city'
        ? [activeTab.dimension, 'country']
        : [activeTab.dimension]
      : [],
    orderBy: activeTab
      ? [{ column: activeTab.metric, direction: 'desc' }]
      : query.orderBy,
    limit: 10,
  }
  const result = useStats(cardQuery)
  const privacy = result.data?.meta?.privacy
  const verified = !verifiedPaths || (privacy?.view === 'verified_paths' && privacy.coverage === 'verified_only' && privacy.unverified_excluded && privacy.imports_excluded)
  const rows = verified ? result.data?.rows ?? [] : []
  const displayedMetrics = (activeTab?.metrics ?? (activeTab ? [activeTab.metric] : []))
    .filter((metric) => metric !== 'pageviews')
  const maximum = rows.reduce((largest, row) => {
    const value = activeTab ? Number(row[activeTab.metric] ?? 0) : 0
    return Math.max(largest, Number.isFinite(value) ? value : 0)
  }, 0)
  const inspectControl = (dimension: string, label: string, country: string, accessibleLabel: string) => {
    const target = inspectTargetFor(dimension, dimension === 'region' || dimension === 'city' ? [label, country] : [label])
    if (!inspect || !target || !inspect.can(target)) return null
    return <InspectButton label={ui.inspector.inspectValue(accessibleLabel)} pressed={sameInspect(inspect.selected, target)} onInspect={(origin) => inspect.open(target, origin)} />
  }

  const linkableDimension = (dimension: string) => ['page', 'entry', 'exit', 'source', 'referrer'].includes(dimension)
  const reserveActions = !verifiedPaths && (Boolean(inspect) || (linkableDimension(activeTab?.dimension ?? '') && (siteHost !== undefined || activeTab?.dimension === 'source')))
  const rowActions = (dimension: string, label: string, country: string, accessibleLabel: string, withInspect = true) => {
    const link = rowLink(dimension, label, siteHost)
    const inspectButton = withInspect ? inspectControl(dimension, label, country, accessibleLabel) : null
    return (
      <TopListActions reserve={reserveActions}>
        {link ? <OpenLinkAction link={link} /> : null}
        {inspectButton}
      </TopListActions>
    )
  }
  const leading = (dimension: string, label: string, country: string) => {
    if (dimension === 'country' || dimension === 'region' || dimension === 'city') return <Flag code={country} />
    if (dimension === 'source') return <SourceIcon value={label} />
    return null
  }

  return (
    <section aria-label={title} className="min-w-0 rounded-lg border bg-card text-card-foreground" role="region">
      <header className="flex min-h-12 items-center justify-between gap-3 border-b px-4 py-1.5">
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <h2 className="shrink-0 truncate text-sm font-medium">{title}</h2>
          {tabs.length > 1 ? (
            <div className="min-w-0 overflow-x-auto">
              <SegmentedControl
                aria-label={ui.breakdown.breakdownOf(title)}
                options={tabs.map((tab) => ({ value: tab.key, label: tab.label ?? tab.key }))}
                panelId={panelId}
                size="sm"
                value={activeTab?.key ?? ''}
                onValueChange={setActiveKey}
              />
            </div>
          ) : null}
        </div>
        {activeTab && !verifiedPaths ? (
          <Button ref={fullReportRef} aria-haspopup="dialog" aria-label={dashboardView.viewAllLabel(title)} className="shrink-0" size="sm" type="button" variant="ghost" onClick={() => setFullReportOpen(true)}>
            {dashboardView.viewAll}
          </Button>
        ) : null}
      </header>
      <div id={panelId} role="tabpanel">
      {description ? <p className="px-4 py-2 text-xs text-muted-foreground">{description}</p> : null}
      {result.isError ? (
        <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
      ) : result.isPending || result.isLoading ? (
        <div className="flex min-h-36 flex-col gap-3 p-4" role="status" aria-label={ui.breakdown.loading(title)}>
          <Skeleton className="h-7 w-full" />
          <Skeleton className="h-7 w-4/5" />
          <Skeleton className="h-7 w-3/5" />
        </div>
      ) : !verified ? (
        <ErrorState detail={ui.breakdown.unverifiedCoverage} onRetry={() => void result.refetch()} />
      ) : rows.length === 0 || !activeTab ? (
        verifiedPaths ? <p className="px-4 py-6 text-sm text-muted-foreground">{ui.breakdown.noVerifiedData}</p> : <EmptyState />
      ) : displayedMetrics.length > 1 ? (
        <div className="min-w-0 overflow-x-auto p-2">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-2 py-2 font-medium">{metricLabel(activeTab.dimension)}</th>
                {displayedMetrics.map((metric) => (
                  <th key={metric} className="px-3 py-2 text-right font-medium">{metricLabel(metric)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => {
                const label = String(row[activeTab.dimension] ?? '')
                const display = breakdownLabel(activeTab.dimension, label)
                const placeCountry = String(row.country ?? '')
                const placeWithoutCountry = (activeTab.dimension === 'region' || activeTab.dimension === 'city') && placeCountry === ''
                const rawValue = Number(row[activeTab.metric] ?? 0)
                const width = barWidth(Number.isFinite(rawValue) ? rawValue : 0, maximum)
                const accessible = filterAccessibleLabel(activeTab.dimension, label, display, placeCountry)
                return (
                  <tr key={`${label}:${index}`} className={topListRowClass}>
                    <td className="relative min-w-0 px-1 py-1">
                      <TopListBar width={width} />
                      {label === '' || !allowFilter || placeWithoutCountry ? (
                        <span className="relative flex min-w-0 items-center gap-2 px-2 py-2 font-medium text-foreground">
                          <RowLabel className="text-foreground" dimension={activeTab.dimension} value={label} display={display} />
                        </span>
                      ) : (
                        <>
                        <span className="sr-only" id={`${filterDescriptionId}-${index}`}>
                          {ui.breakdown.filterBy(activeTab.dimension, accessible)}
                        </span>
                        <div className="relative flex min-w-0 items-center gap-1">
                          <button
                            aria-describedby={`${filterDescriptionId}-${index}`}
                            className="h-8 min-w-0 flex-1 rounded-md px-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
                            type="button"
                            onClick={() => onFilter({
                              dimension: activeTab.dimension,
                              operator: 'is',
                              values: activeTab.dimension === 'region' || activeTab.dimension === 'city'
                                ? [label, placeCountry]
                                : [label],
                            })}
                          >
                            <span className="block min-w-0 truncate font-medium" title={display}>{display}</span>
                          </button>
                          {rowActions(activeTab.dimension, label, placeCountry, accessible)}
                        </div>
                        </>
                      )}
                    </td>
                    {displayedMetrics.map((metric) => (
                      <td key={metric} className="px-3 py-2 text-right tabular-nums text-muted-foreground">
                        {formatBreakdownMetric(metric, row)}
                      </td>
                    ))}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <ol className="flex min-w-0 flex-col p-2">
          {rows.map((row, index) => {
            const label = String(row[activeTab.dimension] ?? '')
            const display = breakdownLabel(activeTab.dimension, label)
            const country = String(row.country ?? (activeTab.dimension === 'country' ? label : ''))
            const placeWithoutCountry = (activeTab.dimension === 'region' || activeTab.dimension === 'city') && country === ''
            const rawValue = Number(row[activeTab.metric] ?? 0)
            const value = Number.isFinite(rawValue) ? rawValue : 0
            const width = barWidth(value, maximum)
            const accessible = filterAccessibleLabel(activeTab.dimension, label, display, country)
            const icon = leading(activeTab.dimension, label, country)
            return (
              <li key={`${label}:${index}`} className="min-w-0">
                {label === '' || !allowFilter || placeWithoutCountry ? (
                  <div className={`${topListRowClass} flex w-full min-w-0 items-center gap-1`}>
                    <div className="relative grid h-8 min-w-0 flex-1 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-2 text-left text-foreground">
                      <TopListBar width={width} />
                      <span className="relative flex min-w-0 items-center gap-2 text-sm text-foreground">
                        {icon}
                        <RowLabel className="text-foreground" dimension={activeTab.dimension} value={label} display={display} />
                      </span>
                      <span className="relative text-right text-sm tabular-nums">{fmt.number(value)}</span>
                    </div>
                    {rowActions(activeTab.dimension, label, country, accessible, false)}
                  </div>
                ) : (
                  <>
                  <span className="sr-only" id={`${filterDescriptionId}-${index}`}>
                    {ui.breakdown.filterBy(activeTab.dimension, accessible)}
                  </span>
                  <div className={`${topListRowClass} flex min-w-0 items-center gap-1`}>
                    <button
                      aria-describedby={`${filterDescriptionId}-${index}`}
                      className="relative grid h-8 min-w-0 flex-1 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-md px-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      type="button"
                      onClick={() =>
                        onFilter({
                          dimension: activeTab.dimension,
                          operator: 'is',
                          values: activeTab.dimension === 'region' || activeTab.dimension === 'city'
                            ? [label, country]
                            : [label],
                        })
                      }
                    >
                      <TopListBar width={width} />
                      <span className="relative flex min-w-0 items-center gap-2 text-sm">
                        {icon}
                        <span className="min-w-0 truncate" title={display}>{display}</span>
                      </span>
                      <span className="relative text-right text-sm tabular-nums text-foreground">
                        {fmt.number(value)}
                      </span>
                    </button>
                    {rowActions(activeTab.dimension, label, country, accessible)}
                  </div>
                  </>
                )}
              </li>
            )
          })}
        </ol>
      )}
      </div>
      {fullReportOpen && activeTab ? (
        <Suspense fallback={null}>
          <FullReportDialog dimension={activeTab.dimension} query={cardQuery} title={title} allowSearch={allowSearch} returnFocusRef={fullReportRef} onClose={() => setFullReportOpen(false)} />
        </Suspense>
      ) : null}
    </section>
  )
}
