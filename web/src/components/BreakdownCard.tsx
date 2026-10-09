import { lazy, Suspense, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { countryLabel } from '@/lib/country'
import { formatBreakdownMetric, metricLabel } from '@/lib/breakdown-metrics'
import { fmt } from '@/lib/format'
import type { Filter } from '@/lib/search'
import { useStats, type StatsQuery } from '@/lib/stats'

import { DetailsDialog } from './DetailsDialog'
import { EmptyState } from './EmptyState'
import { ErrorState } from './ErrorState'
import { Flag } from './Flag'

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
  map?: boolean
  allowSearch?: boolean
  allowFilter?: boolean
  verifiedPaths?: boolean
  description?: string
}

const WorldMap = lazy(() => import('./WorldMap').then((module) => ({ default: module.WorldMap })))

const screenSizeLabels: Record<string, string> = {
  mobile: 'Mobile (<768px)',
  tablet: 'Tablet (768–1023px)',
  laptop: 'Laptop (1024–1439px)',
  desktop: 'Desktop (1440–1919px)',
  large: 'Large (≥1920px)',
}

function breakdownLabel(dimension: string, value: string): string {
  if (value === '') return 'Unknown'
  if (dimension === 'country') return countryLabel(value)
  if (dimension === 'screen_size') return screenSizeLabels[value] ?? value
  return value
}

export function BreakdownCard({ title, tabs, query, onFilter, map = false, allowSearch = true, allowFilter = true, verifiedPaths = false, description }: BreakdownCardProps) {
  const [activeKey, setActiveKey] = useState(tabs[0]?.key ?? '')
  const [view, setView] = useState<'list' | 'map'>('list')
  const activeTab = tabs.find((tab) => tab.key === activeKey) ?? tabs[0]
  const mapActive = map && activeTab?.dimension === 'country' && view === 'map'
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
    limit: mapActive ? 250 : 10,
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

  return (
    <section aria-label={title} className="min-w-0 rounded-xl border bg-card text-card-foreground" role="region">
      <header className="flex min-h-12 items-center justify-between gap-3 border-b px-4">
        <div className="flex min-w-0 flex-1 items-center gap-3">
          <h2 className="shrink-0 truncate text-sm font-medium">{title}</h2>
          {tabs.length > 1 ? (
            <div aria-label={`${title} breakdown`} className="flex min-w-0 items-center gap-1 overflow-x-auto" role="tablist">
              {tabs.map((tab) => (
                <Button
                  key={tab.key}
                  aria-selected={tab.key === activeTab?.key}
                  role="tab"
                  size="xs"
                  type="button"
                  variant={tab.key === activeTab?.key ? 'secondary' : 'ghost'}
                  onClick={() => {
                    setActiveKey(tab.key)
                    if (tab.dimension !== 'country') setView('list')
                  }}
                >
                  {tab.label ?? tab.key}
                </Button>
              ))}
            </div>
          ) : null}
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {map && activeTab?.dimension === 'country' ? (
            <div aria-label={`${title} view`} className="flex items-center gap-1" role="group">
              <Button
                aria-pressed={view === 'list'}
                size="xs"
                type="button"
                variant={view === 'list' ? 'secondary' : 'ghost'}
                onClick={() => setView('list')}
              >
                List
              </Button>
              <Button
                aria-pressed={view === 'map'}
                size="xs"
                type="button"
                variant={view === 'map' ? 'secondary' : 'ghost'}
                onClick={() => setView('map')}
              >
                Map
              </Button>
            </div>
          ) : null}
          {activeTab && !verifiedPaths ? <DetailsDialog dimension={activeTab.dimension} query={cardQuery} title={title} allowSearch={allowSearch} /> : null}
        </div>
      </header>
      {description ? <p className="border-b px-4 py-3 text-sm text-muted-foreground">{description}</p> : null}
      {result.isError ? (
        <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
      ) : result.isPending || result.isLoading ? (
        <div className="flex min-h-36 flex-col gap-3 p-4" aria-label={`Loading ${title}`}>
          <Skeleton className="h-7 w-full" />
          <Skeleton className="h-7 w-4/5" />
          <Skeleton className="h-7 w-3/5" />
        </div>
      ) : !verified ? (
        <ErrorState detail="Verified page coverage could not be confirmed. Reload before viewing this breakdown." onRetry={() => void result.refetch()} />
      ) : rows.length === 0 || !activeTab ? (
        verifiedPaths ? <p className="px-4 py-6 text-sm text-muted-foreground">No verified page data in this range. Older unverified records and imports are excluded.</p> : <EmptyState />
      ) : mapActive ? (
        <Suspense
          fallback={
            <div className="min-h-36 p-4" aria-label={`Loading ${title} map`}>
              <Skeleton className="h-32 w-full" />
            </div>
          }
        >
          <WorldMap
            rows={rows.map((row) => ({
              country: String(row.country ?? ''),
              visitors: Number(row.visitors ?? 0),
            }))}
            onSelect={(code) => onFilter({ dimension: 'country', operator: 'is', values: [code] })}
          />
        </Suspense>
      ) : displayedMetrics.length > 1 ? (
        <div className="min-w-0 overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-4 py-2 font-medium">{metricLabel(activeTab.dimension)}</th>
                {displayedMetrics.map((metric) => (
                  <th key={metric} className="px-3 py-2 text-right font-medium">{metricLabel(metric)}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => {
                const label = String(row[activeTab.dimension] ?? '')
                const display = breakdownLabel(activeTab.dimension, label)
                return (
                  <tr key={`${label}:${index}`} className="border-t">
                    <td className="min-w-0 px-2 py-1">
                      {label === '' || !allowFilter ? (
                        <span className="block min-w-0 px-2 py-2 font-medium text-muted-foreground">{display}</span>
                      ) : (
                        <button
                          aria-label={`Filter by ${activeTab.dimension} ${activeTab.dimension === 'country' ? display : label}`}
                          className="w-full min-w-0 rounded-lg px-2 py-2 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring"
                          type="button"
                          onClick={() => onFilter({ dimension: activeTab.dimension, operator: 'is', values: [label] })}
                        >
                          <span className="block min-w-0 truncate font-medium" title={display}>{display}</span>
                        </button>
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
            const rawValue = Number(row[activeTab.metric] ?? 0)
            const value = Number.isFinite(rawValue) ? rawValue : 0
            const width = maximum > 0 ? `${Math.max(2, (value / maximum) * 100)}%` : '0%'
            return (
              <li key={`${label}:${index}`} className="min-w-0">
                {label === '' || !allowFilter ? (
                  <div className="relative grid w-full min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-lg px-2 py-2 text-left text-muted-foreground">
                    <span aria-hidden="true" className="absolute inset-y-1 left-0 rounded-md bg-accent/10" style={{ width }} />
                    <span className="relative min-w-0 truncate text-sm" title={display}>{display}</span>
                    <span className="relative text-sm tabular-nums">{fmt.number(value)}</span>
                  </div>
                ) : (
                  <button
                    aria-label={`Filter by ${activeTab.dimension} ${activeTab.dimension === 'country' ? display : label}`}
                    className="relative grid w-full min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-lg px-2 py-2 text-left outline-none hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-ring"
                    type="button"
                    onClick={() =>
                      onFilter({
                        dimension: activeTab.dimension,
                        operator: 'is',
                        values: [label],
                      })
                    }
                  >
                    <span aria-hidden="true" className="absolute inset-y-1 left-0 rounded-md bg-accent/10" style={{ width }} />
                    <span className="relative flex min-w-0 items-center gap-2 text-sm">
                      {activeTab.dimension === 'country' || activeTab.dimension === 'region' || activeTab.dimension === 'city'
                        ? <Flag code={country} />
                        : null}
                      <span className="min-w-0 truncate" title={display}>{display}</span>
                    </span>
                    <span className="relative text-sm tabular-nums text-muted-foreground">
                      {fmt.number(value)}
                    </span>
                  </button>
                )}
              </li>
            )
          })}
        </ol>
      )}
    </section>
  )
}
