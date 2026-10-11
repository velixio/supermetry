import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { ErrorState } from '@/components/ErrorState'
import { FilterBar } from '@/components/FilterBar'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { CountryDetails } from '@/features/map/CountryDetails'
import { CountryTable, type CountryMetricRow } from '@/features/map/CountryTable'
import { TrafficGrid } from '@/features/map/TrafficGrid'
import type { TrafficMetric } from '@/features/map/trafficGrid'
import { countryName } from '@/lib/country'
import { useBreakdown, type StatsQuery } from '@/lib/stats'
import { addFilter, dashboardSearch, parseFilters, removeFilter, replaceFilters, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './app.$org'

const WorldMap = lazy(() =>
  import('@/components/WorldMap').then((module) => ({
    default: module.WorldMap,
  })),
)

const mapSearch = dashboardSearch.extend({
  metric: z.enum(['visitors', 'pageviews']).catch('visitors'),
  country: z
    .string()
    .regex(/^[A-Za-z]{2}$/)
    .transform((value) => value.toUpperCase())
    .optional()
    .catch(undefined),
})

export const Route = createFileRoute('/app/$org/$site/map')({
  validateSearch: (search) => mapSearch.parse(search),
  component: MapRoute,
})

function MapRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find(
    (item) => item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  const { filters, dropped } = parseFilters(search.f)
  if (!site) return null

  const timezone = site.timezone || 'UTC'
  const metric = search.metric as TrafficMetric
  const query: StatsQuery = {
    siteId: site.id,
    dateRange: resolveRange(search.period, timezone, new Date(), search.from, search.to),
    metrics: [metric],
    filters,
    compare: search.compare,
    timezone,
    trackingMode: site.tracking_policy?.effective_mode,
    includeImported: search.include_imported,
  }
  const countries = useBreakdown({
    ...query,
    metrics: ['visitors', 'pageviews'],
    dimensions: ['country'],
    limit: 250,
  })
  const traffic = useBreakdown({
    ...query,
    dimensions: ['weekday', 'hour_of_day'],
    limit: 168,
  })
  const countryRows: CountryMetricRow[] = (countries.data?.rows ?? []).map((row) => ({
    country: String(row.country ?? ''),
    visitors: Number(row.visitors ?? 0),
    pageviews: Number(row.pageviews ?? 0),
  }))
  const updateSearch = (next: typeof search) => void navigate({ search: next })
  const selectCountry = (country: string) => updateSearch({ ...search, country })
  const clearCountry = () => updateSearch({ ...search, country: undefined })
  const dashboardSearchState = {
    period: search.period,
    from: search.from,
    to: search.to,
    compare: search.compare,
    f: search.f,
    metric: search.metric,
    live: search.live,
    include_imported: search.include_imported,
  }

  return (
    <div className="flex min-w-0 flex-col gap-4" data-filter-count={filters.length}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold">{en.map.title}</h1>
          <p className="text-sm text-muted-foreground">{en.map.mapDescription}</p>
        </div>
        <ToggleGroup
          aria-label={en.map.title}
          type="single"
          value={metric}
          onValueChange={(value) => {
            if (value === 'visitors' || value === 'pageviews') updateSearch({ ...search, metric: value })
          }}
        >
          <ToggleGroupItem value="visitors">{en.map.visitors}</ToggleGroupItem>
          <ToggleGroupItem value="pageviews">{en.map.pageviews}</ToggleGroupItem>
        </ToggleGroup>
      </div>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <FilterBar
          siteId={site.id}
          orgId={membership?.org.id}
          canEdit={membership?.role !== 'viewer'}
          dateRange={query.dateRange}
          filters={filters}
          dropped={dropped}
          invalidNotice={en.dashboard.invalidFilter}
          filterLabel={(filter) =>
            `${filter.dimension} ${filter.operator} ${filter.values.map((value) => (filter.dimension === 'country' ? countryName(value) : value)).join(', ')}`
          }
          removeLabel={(filter) =>
            en.dashboard.filterLabel
              .replace('{dimension}', filter.dimension)
              .replace('{value}', filter.values.join(', '))
          }
          onApply={(nextFilters) =>
            updateSearch({
              ...search,
              ...replaceFilters(dashboardSearchState, nextFilters),
              metric,
              country: search.country,
            })
          }
          onAdd={(filter) =>
            updateSearch({
              ...search,
              ...addFilter(dashboardSearchState, filter),
              metric,
              country: search.country,
            })
          }
          onRemove={(index) =>
            updateSearch({
              ...search,
              ...removeFilter(dashboardSearchState, index),
              metric,
              country: search.country,
            })
          }
        />
        <DatePicker
          labels={en.dashboard}
          search={dashboardSearchState}
          onChange={(next) =>
            updateSearch({
              ...search,
              ...next,
              metric,
              country: search.country,
            })
          }
        />
      </div>
      <div className="grid min-w-0 grid-cols-1 items-stretch gap-4 lg:grid-cols-3">
        <Card className="min-w-0 lg:col-span-2 lg:min-h-[39rem]">
          <CardHeader className="border-b">
            <CardTitle>{en.map.countries}</CardTitle>
            <CardDescription>{en.map.mapDescription}</CardDescription>
          </CardHeader>
          <CardContent className="flex min-h-0 flex-1 flex-col justify-center pt-4">
            {countries.isPending ? (
              <Skeleton aria-label={en.map.detailsLoading} className="aspect-2/1 w-full" />
            ) : countries.isError ? (
              <ErrorState detail={countries.error?.detail} onRetry={() => void countries.refetch()} />
            ) : (
              <Suspense fallback={<Skeleton className="aspect-2/1 w-full" />}>
                <WorldMap
                  rows={countryRows}
                  metric={metric}
                  selectedCountry={search.country}
                  showLegend
                  onClear={clearCountry}
                  onSelect={selectCountry}
                />
              </Suspense>
            )}
          </CardContent>
        </Card>
        <div className="min-w-0 lg:min-h-[39rem]">
          <TrafficGrid
            rows={traffic.data?.rows ?? []}
            metric={metric}
            timezone={timezone}
            isLoading={traffic.isPending}
            error={traffic.error}
            onRetry={() => void traffic.refetch()}
            onShorten={(days) =>
              updateSearch({
                ...search,
                period: days <= 7 ? '7d' : '30d',
                from: undefined,
                to: undefined,
              })
            }
          />
        </div>
      </div>
      {search.country ? (
        <CountryDetails
          country={search.country}
          query={query}
          onClear={clearCountry}
          onFilter={() =>
            void navigate({
              to: '/app/$org/$site',
              params: { org, site: siteSlug },
              search: addFilter(dashboardSearchState, {
                dimension: 'country',
                operator: 'is',
                values: [search.country ?? ''],
              }),
            })
          }
        />
      ) : null}
      {countries.isSuccess ? (
        <CountryTable rows={countryRows} metric={metric} selectedCountry={search.country} onSelect={selectCountry} />
      ) : null}
    </div>
  )
}
