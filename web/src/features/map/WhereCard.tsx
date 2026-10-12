import { lazy, Suspense, useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { EmptyState } from '@/components/EmptyState'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { countryLabel } from '@/lib/country'
import { fmt } from '@/lib/format'
import { useBreakdown, type StatsQuery } from '@/lib/stats'
import type { Filter } from '@/lib/search'
import { en } from '@/messages/en'

import { CountryDetails } from './CountryDetails'
import { CountryTable, type CountryMetricRow } from './CountryTable'
import type { TrafficMetric } from './trafficGrid'

const WorldMap = lazy(() => import('@/components/WorldMap').then((module) => ({ default: module.WorldMap })))

type WhereCardProps = {
  query: StatsQuery
  metric: TrafficMetric
  onFilter(filter: Filter): void
}

export function WhereCard({ query, metric, onFilter }: WhereCardProps) {
  const [selectedCountry, setSelectedCountry] = useState<string>()
  const [activeCountry, setActiveCountry] = useState<string>()
  const countries = useBreakdown({ ...query, metrics: ['visitors', 'pageviews'], dimensions: ['country'], limit: 250 })
  const rows: CountryMetricRow[] = (countries.data?.rows ?? []).map((row) => ({
    country: String(row.country ?? ''),
    visitors: Number(row.visitors ?? 0),
    pageviews: Number(row.pageviews ?? 0),
  }))
  const ranked = [...rows].filter((row) => row[metric] > 0).sort((left, right) => right[metric] - left[metric])
  const total = ranked.reduce((sum, row) => sum + row[metric], 0)
  const leader = ranked[0]
  const low = ranked.at(-1)
  const metricLabel = metric === 'visitors' ? 'visitors' : 'pageviews'
  const countryValue = (row: CountryMetricRow | undefined) => row
    ? en.a11y2.countryValue.replace('{country}', countryLabel(row.country)).replace('{value}', fmt.number(row[metric]))
    : '0'
  const summary = en.a11y2.dataSummary
    .replace('{title}', 'Where')
    .replace('{from}', fmt.date(query.dateRange.from, query.timezone ?? 'UTC'))
    .replace('{to}', fmt.date(query.dateRange.to, query.timezone ?? 'UTC'))
    .replace('{total}', `${fmt.number(total)} ${metricLabel}`)
    .replace('{peak}', countryValue(leader))
    .replace('{low}', countryValue(low))
  const leaderHeadline = leader
    ? en.a11y2.whereLeader
      .replace('{country}', countryLabel(leader.country))
      .replace('{value}', fmt.number(leader[metric]))
      .replace('{metric}', metricLabel)
    : undefined

  return (
    <Card className="min-w-0 overflow-hidden" aria-labelledby="where-title">
      <CardHeader className="border-b">
        <CardTitle id="where-title">Where</CardTitle>
        <CardDescription className="sr-only" id="where-summary">{summary}</CardDescription>
        {leaderHeadline ? <p className="text-sm font-medium">{leaderHeadline}</p> : null}
      </CardHeader>
      <CardContent className="p-0">
        {countries.isPending ? (
          <div className="grid min-h-80 gap-4 p-4 lg:grid-cols-[minmax(0,3fr)_minmax(9rem,2fr)]" role="status" aria-label="Loading geography">
            <Skeleton className="aspect-2/1 w-full self-center" />
            <div className="space-y-2">{Array.from({ length: 8 }, (_, index) => <Skeleton key={index} className="h-9 w-full" />)}</div>
          </div>
        ) : countries.isError ? (
          <div className="min-h-80 p-4"><ErrorState detail={countries.error?.detail} onRetry={() => void countries.refetch()} /></div>
        ) : ranked.length === 0 ? (
          <div className="min-h-80" role="status"><EmptyState /></div>
        ) : (
          <div className="grid min-h-80 min-w-0 items-start gap-4 p-4 xl:grid-cols-[minmax(60%,1fr)_minmax(14rem,40%)]">
            <div className="min-w-0 lg:min-h-[260px]" data-map-column>
              <Suspense fallback={<Skeleton className="aspect-2/1 w-full" />}>
                <WorldMap
                  activeCountry={activeCountry}
                  describedBy="where-summary"
                  metric={metric}
                  onActiveChange={setActiveCountry}
                  onClear={() => setSelectedCountry(undefined)}
                  onSelect={setSelectedCountry}
                  rows={rows}
                  selectedCountry={selectedCountry}
                  showLegend
                />
              </Suspense>
            </div>
            <div className="min-w-0 self-start xl:min-w-56">
              <CountryTable
                activeCountry={activeCountry}
                metric={metric}
                onActiveChange={setActiveCountry}
                onSelect={setSelectedCountry}
                rows={rows}
                selectedCountry={selectedCountry}
              />
            </div>
          </div>
        )}
        {selectedCountry ? (
          <CountryDetails
            country={selectedCountry}
            query={{ ...query, metrics: [metric] }}
            onClear={() => setSelectedCountry(undefined)}
            onFilter={() => onFilter({ dimension: 'country', operator: 'is', values: [selectedCountry] })}
          />
        ) : null}
      </CardContent>
    </Card>
  )
}
