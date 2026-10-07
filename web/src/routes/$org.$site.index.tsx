import { createFileRoute } from '@tanstack/react-router'

import { BreakdownCard, type BreakdownTab } from '../components/BreakdownCard'
import { DatePicker } from '../components/DatePicker'
import { FilterBar } from '../components/FilterBar'
import { MainChart } from '../components/MainChart'
import { DashboardNoDataBanner } from '../components/NoDataBanner'
import { StatsStrip } from '../components/StatsStrip'
import { useShortcuts } from '../lib/shortcuts'
import type { StatsQuery } from '../lib/stats'
import {
  addFilter,
  dashboardSearch,
  parseFilters,
  removeFilter,
  resolveRange,
  type Filter,
} from '../lib/search'
import { en } from '../messages/en'
import { useMe } from './$org'

export const Route = createFileRoute('/$org/$site/')({
  validateSearch: (search) => dashboardSearch.parse(search),
  component: DashboardStateRoute,
})

function DashboardStateRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const { filters, dropped } = parseFilters(search.f)
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find(
    (item) =>
      item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  const updateSearch = (next: typeof search) => {
    void navigate({ search: next })
  }

  useShortcuts({
    m: () => updateSearch({ ...search, period: '30d', from: undefined, to: undefined }),
    c: () =>
      updateSearch({
        ...search,
        compare: search.compare === 'off' ? 'previous_period' : 'off',
      }),
  })

  if (!site) return <p className="sr-only">{en.loadingDashboard}</p>

  const timezone = site.timezone || 'UTC'

  const query: StatsQuery = {
    siteId: site.id,
    dateRange: resolveRange(
      search.period,
      timezone,
      new Date(),
      search.from,
      search.to,
    ),
    metrics: ['visitors'],
    filters,
    compare: search.compare,
    timezone,
    live: search.live,
  }
  const onFilter = (filter: Filter) => {
    void navigate({ search: addFilter(search, filter) })
  }

  return (
    <div className="flex min-w-0 flex-col gap-4" data-filter-count={filters.length}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <FilterBar
          filters={filters}
          dropped={dropped}
          invalidNotice={en.dashboard.invalidFilter}
          filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.join(', ')}`}
          removeLabel={(filter) =>
            en.dashboard.filterLabel
              .replace('{dimension}', filter.dimension)
              .replace('{value}', filter.values.join(', '))
          }
          onRemove={(index) => updateSearch(removeFilter(search, index))}
        />
        <DatePicker
          search={search}
          labels={en.dashboard}
          onChange={updateSearch}
        />
      </div>
      <StatsStrip
        query={query}
        compare={search.compare}
        onSelectMetric={(metric) => updateSearch({ ...search, metric })}
      />
      <DashboardNoDataBanner
        query={query}
        href={`/${org}/${siteSlug}/settings/installation`}
      />
      <MainChart query={query} metric={search.metric} compare={search.compare} />
      <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
        {breakdownCards.map((card) => (
          <BreakdownCard
            key={card.title}
            title={card.title}
            tabs={card.tabs}
            query={query}
            onFilter={onFilter}
          />
        ))}
      </div>
    </div>
  )
}

const breakdownCards: { title: string; tabs: BreakdownTab[] }[] = [
  {
    title: 'Pages',
    tabs: [
      { key: 'top', label: 'Top', dimension: 'page', metric: 'visitors' },
      { key: 'entry', label: 'Entry', dimension: 'entry', metric: 'visitors' },
      { key: 'exit', label: 'Exit', dimension: 'exit', metric: 'visitors' },
    ],
  },
  {
    title: 'Sources',
    tabs: [
      { key: 'source', label: 'Source', dimension: 'source', metric: 'visitors' },
      { key: 'medium', label: 'Medium', dimension: 'utm_medium', metric: 'visitors' },
      { key: 'campaign', label: 'Campaign', dimension: 'utm_campaign', metric: 'visitors' },
    ],
  },
  {
    title: 'Countries',
    tabs: [
      { key: 'country', label: 'Countries', dimension: 'country', metric: 'visitors' },
      { key: 'region', label: 'Regions', dimension: 'region', metric: 'visitors' },
      { key: 'city', label: 'Cities', dimension: 'city', metric: 'visitors' },
    ],
  },
  {
    title: 'Devices',
    tabs: [
      { key: 'browser', label: 'Browser', dimension: 'browser', metric: 'visitors' },
      { key: 'os', label: 'OS', dimension: 'os', metric: 'visitors' },
      { key: 'device', label: 'Device', dimension: 'device', metric: 'visitors' },
    ],
  },
  {
    title: 'Goals & events',
    tabs: [
      { key: 'goals', label: 'Goals', dimension: 'goal', metric: 'conversions' },
      { key: 'events', label: 'Events', dimension: 'event', metric: 'events' },
    ],
  },
]
