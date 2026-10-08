import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { countryLabel } from '@/lib/country'
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
}

type BreakdownCardProps = {
  title: string
  tabs: BreakdownTab[]
  query: StatsQuery
  onFilter: (filter: Filter) => void
}

export function BreakdownCard({ title, tabs, query, onFilter }: BreakdownCardProps) {
  const [activeKey, setActiveKey] = useState(tabs[0]?.key ?? '')
  const activeTab = tabs.find((tab) => tab.key === activeKey) ?? tabs[0]
  const cardQuery: StatsQuery = {
    ...query,
    metrics: activeTab ? [activeTab.metric] : query.metrics,
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
  const rows = result.data?.rows ?? []
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
                  onClick={() => setActiveKey(tab.key)}
                >
                  {tab.label ?? tab.key}
                </Button>
              ))}
            </div>
          ) : null}
        </div>
        {activeTab ? <DetailsDialog dimension={activeTab.dimension} query={cardQuery} title={title} /> : null}
      </header>
      {result.isError ? (
        <ErrorState detail={result.error?.detail} onRetry={() => void result.refetch()} />
      ) : result.isPending || result.isLoading ? (
        <div className="flex min-h-36 flex-col gap-3 p-4" aria-label={`Loading ${title}`}>
          <Skeleton className="h-7 w-full" />
          <Skeleton className="h-7 w-4/5" />
          <Skeleton className="h-7 w-3/5" />
        </div>
      ) : rows.length === 0 || !activeTab ? (
        <EmptyState />
      ) : (
        <ol className="flex min-w-0 flex-col p-2">
          {rows.map((row, index) => {
            const label = String(row[activeTab.dimension] ?? '')
            const display = activeTab.dimension === 'country' ? countryLabel(label) : label
            const country = String(row.country ?? (activeTab.dimension === 'country' ? label : ''))
            const rawValue = Number(row[activeTab.metric] ?? 0)
            const value = Number.isFinite(rawValue) ? rawValue : 0
            const width = maximum > 0 ? `${Math.max(2, (value / maximum) * 100)}%` : '0%'
            return (
              <li key={`${label}:${index}`} className="min-w-0">
                <button
                  aria-label={`Filter by ${activeTab.dimension} ${label}`}
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
              </li>
            )
          })}
        </ol>
      )}
    </section>
  )
}
