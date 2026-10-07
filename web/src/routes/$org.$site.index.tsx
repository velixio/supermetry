import { createFileRoute } from '@tanstack/react-router'

import { BreakdownCard, type BreakdownTab } from '../components/BreakdownCard'
import type { StatsQuery } from '../lib/stats'
import {
  addFilter,
  dashboardSearch,
  parseFilters,
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
  const { filters } = parseFilters(search.f)
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find(
    (item) =>
      item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )

  if (!site) return <p className="sr-only">{en.loadingDashboard}</p>

  const query: StatsQuery = {
    siteId: site.id,
    dateRange: resolveRange(
      search.period,
      'UTC',
      new Date(),
      search.from,
      search.to,
    ),
    metrics: ['visitors'],
    filters,
    compare: search.compare,
    timezone: 'UTC',
    live: search.live,
  }
  const onFilter = (filter: Filter) => {
    void navigate({ search: addFilter(search, filter) })
  }

  return (
    <div
      className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2"
      data-filter-count={filters.length}
    >
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
      { key: 'referrer', label: 'Referrer', dimension: 'referrer', metric: 'visitors' },
    ],
  },
  {
    title: 'Countries',
    tabs: [{ key: 'country', dimension: 'country', metric: 'visitors' }],
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
