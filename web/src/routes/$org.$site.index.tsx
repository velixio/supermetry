import { isAnonymousTracking, isAnonymousFilterAllowed, isAnonymousDimensionAllowed, trackingPolicyKnown } from '@/lib/tracking-policy'
import { createFileRoute } from '@tanstack/react-router'

import { BreakdownCard, type BreakdownTab } from '../components/BreakdownCard'
import { DatePicker } from '../components/DatePicker'
import { DashboardExportMenu } from '../components/DashboardExportMenu'
import { FilterBar } from '../components/FilterBar'
import { MainChart } from '../components/MainChart'
import { AnnotationManager } from '../features/annotations/AnnotationManager'
import { DashboardNoDataBanner } from '../components/NoDataBanner'
import { StatsStrip } from '../components/StatsStrip'
import { Button } from '../components/ui/button'
import { useShortcuts } from '../lib/shortcuts'
import { countryName } from '../lib/country'
import type { StatsQuery } from '../lib/stats'
import {
  addFilter,
  dashboardSearch,
  parseFilters,
  removeFilter,
  replaceFilters,
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

  if (!trackingPolicyKnown(site)) {
    return <section className="rounded-xl border bg-card p-5 space-y-3" role="status">
      <h2 className="font-medium">Tracking policy is unavailable</h2>
      <p className="text-sm text-muted-foreground">Reload the policy before opening analytics. No dashboard data has been requested.</p>
      <Button variant="outline" onClick={() => void me.refetch()}>Reload tracking policy</Button>
    </section>
  }
  const anonymous = isAnonymousTracking(site)
  if (anonymous && filters.some((filter) => !isAnonymousFilterAllowed(filter))) {
    return <section className="rounded-xl border bg-card p-5 space-y-3" role="status">
      <h2 className="font-medium">These filters are unavailable in anonymous mode</h2>
      <p className="text-sm text-muted-foreground">The saved URL includes filters that could reveal identifying data. Clear them to view safe aggregate analytics.</p>
      <Button variant="outline" onClick={() => updateSearch(replaceFilters(search, filters.filter(isAnonymousFilterAllowed)))}>Clear unavailable filters</Button>
    </section>
  }
  const timezone = site.timezone || 'UTC'

  const query: StatsQuery = {
    siteId: site.id,
    trackingMode: anonymous ? 'anonymous' : 'identified',
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
    includeImported: search.include_imported,
  }
  const onFilter = (filter: Filter) => {
    if (!anonymous || isAnonymousFilterAllowed(filter)) void navigate({ search: addFilter(search, filter) })
  }

  return (
    <div className="flex min-w-0 flex-col gap-4" data-filter-count={filters.length}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        {anonymous ? <div className="flex flex-wrap items-center gap-2 text-sm">
          <p className="text-muted-foreground">Anonymous analytics: filter by a country, channel, browser, OS, or device row. Saved segments and free-text filters are unavailable.</p>
          {filters.map((filter, index) => <Button key={index} variant="outline" size="sm" onClick={() => updateSearch(removeFilter(search, index))} aria-label={`Remove ${filter.dimension} filter`}>{filter.dimension} {filter.operator} {filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')} ×</Button>)}
          {dropped.length > 0 ? <p role="status">{en.dashboard.invalidFilter}</p> : null}
        </div> : <FilterBar
          siteId={site.id}
          orgId={membership?.org.id}
          canEdit={membership?.role !== 'viewer'}
          dateRange={query.dateRange}
          filters={filters}
          dropped={dropped}
          invalidNotice={en.dashboard.invalidFilter}
          filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
          removeLabel={(filter) =>
            en.dashboard.filterLabel
              .replace('{dimension}', filter.dimension)
              .replace('{value}', filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', '))
          }
          onApply={(segmentFilters) =>
            updateSearch(replaceFilters(search, segmentFilters))
          }
          onAdd={onFilter}
          onRemove={(index) => updateSearch(removeFilter(search, index))}
        />}
        <div className="flex flex-wrap items-center gap-2">
          <DashboardExportMenu query={query} anonymous={anonymous} />
          <DatePicker
            search={search}
            labels={en.dashboard}
            onChange={updateSearch}
          />
        </div>
      </div>
      <Button
        className="self-start"
        type="button"
        size="sm"
        variant="outline"
        aria-pressed={search.include_imported}
        onClick={() => updateSearch({ ...search, include_imported: !search.include_imported })}
      >
        Include imported data
      </Button>
      <StatsStrip
        query={query}
        compare={search.compare}
        onSelectMetric={(metric) => updateSearch({ ...search, metric })}
      />
      <DashboardNoDataBanner
        query={query}
        href={`/${org}/${siteSlug}/settings/installation`}
      />
      {anonymous ? <MainChart query={query} metric={search.metric} compare={search.compare} /> : <AnnotationManager
        siteId={site.id}
        dateRange={query.dateRange}
        canEdit={membership?.role !== 'viewer'}
      >
        {({ annotations, onEdit, onDelete }) => (
          <MainChart
            query={query}
            metric={search.metric}
            compare={search.compare}
            annotations={annotations}
            canEditAnnotations={membership?.role !== 'viewer'}
            onEditAnnotation={onEdit}
            onDeleteAnnotation={onDelete}
          />
        )}
      </AnnotationManager>}
      <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
        {breakdownCards.map((card) => {
          const page = anonymous && card.title === 'Pages'
          const tabs = anonymous
            ? page ? [{ key: 'verified', label: 'Verified', dimension: 'page', metric: 'visitors' as const, metrics: ['visitors', 'visits', 'pageviews'] as StatsQuery['metrics'] }]
              : card.tabs.filter((tab) => isAnonymousDimensionAllowed(tab.dimension))
            : visibleTabs(card.tabs, site.allowed_hosts)
          if (tabs.length === 0 || (page && filters.length > 0)) {
            return <section key={card.title} aria-label={page ? 'Verified pages' : card.title} className="rounded-xl border bg-card p-4" role="region">
              <h2 className="text-sm font-medium">{page ? 'Verified pages' : card.title}</h2>
              <p className="mt-3 text-sm text-muted-foreground">{page ? 'Verified page analytics require no filters. Remove active filters to view this partial dataset.' : 'Goals and event breakdowns are unavailable in anonymous mode because their labels may contain identifying data.'}</p>
            </section>
          }
          return <BreakdownCard
            key={card.title}
            map={card.map}
            title={page ? 'Verified pages' : anonymous && card.title === 'Sources' ? 'Channels' : card.title}
            tabs={tabs}
            query={page ? { ...query, includeImported: false } : query}
            onFilter={onFilter}
            allowSearch={!anonymous}
            allowFilter={!page}
            verifiedPaths={page}
            description={anonymous ? page ? 'Partial coverage: only verified page data under the current path policy. Historical unverified data and imports are excluded. Page filters and detailed searches are unavailable.' : 'Only canonical categories are available. Free-text labels, versions, and searches are restricted in anonymous mode.' : undefined}
          />
        })}
      </div>
    </div>
  )
}

const breakdownCards: { title: string; tabs: BreakdownTab[]; map?: boolean }[] = [
  {
    title: 'Pages',
    tabs: [
      {
        key: 'top',
        label: 'Top',
        dimension: 'page',
        metric: 'visitors',
        metrics: ['visitors', 'pageviews', 'time_on_page', 'exit_rate'],
      },
      { key: 'entry', label: 'Entry', dimension: 'entry', metric: 'visitors' },
      { key: 'exit', label: 'Exit', dimension: 'exit', metric: 'visitors' },
    ],
  },
  {
    title: 'Sources',
    tabs: [
      { key: 'source', label: 'Source', dimension: 'source', metric: 'visitors' },
      { key: 'channel', label: 'Channels', dimension: 'channel', metric: 'visitors' },
      { key: 'utm-source', label: 'UTM Source', dimension: 'utm_source', metric: 'visitors' },
      { key: 'medium', label: 'Medium', dimension: 'utm_medium', metric: 'visitors' },
      { key: 'campaign', label: 'Campaign', dimension: 'utm_campaign', metric: 'visitors' },
    ],
  },
  {
    title: 'Countries',
    map: true,
    tabs: [
      { key: 'country', label: 'Countries', dimension: 'country', metric: 'visitors' },
      { key: 'region', label: 'Regions', dimension: 'region', metric: 'visitors' },
      { key: 'city', label: 'Cities', dimension: 'city', metric: 'visitors' },
      { key: 'language', label: 'Language', dimension: 'language', metric: 'visitors' },
      { key: 'hostname', label: 'Hostname', dimension: 'hostname', metric: 'visitors' },
    ],
  },
  {
    title: 'Devices',
    tabs: [
      { key: 'browser', label: 'Browser', dimension: 'browser', metric: 'visitors' },
      { key: 'browser-version', label: 'Browser version', dimension: 'browser_version', metric: 'visitors' },
      { key: 'os', label: 'OS', dimension: 'os', metric: 'visitors' },
      { key: 'os-version', label: 'OS version', dimension: 'os_version', metric: 'visitors' },
      { key: 'device', label: 'Device', dimension: 'device', metric: 'visitors' },
      { key: 'screen-size', label: 'Screen size', dimension: 'screen_size', metric: 'visitors' },
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

function visibleTabs(tabs: BreakdownTab[], allowedHosts: string[] | undefined): BreakdownTab[] {
  const showHostname =
    (allowedHosts?.length ?? 0) > 1 ||
    allowedHosts?.some((host) => host.startsWith('*.')) === true
  return showHostname ? tabs : tabs.filter((tab) => tab.dimension !== 'hostname')
}
