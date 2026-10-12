import { BreakdownCard, type BreakdownTab } from '@/components/BreakdownCard'
import { WeeklyRhythmCard } from '@/features/map/WeeklyRhythmCard'
import { WhereCard } from '@/features/map/WhereCard'
import { isAnonymousDimensionAllowed } from '@/lib/tracking-policy'
import type { DashboardMetric, Filter } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'

export function DashboardBelowFold({
  allowedHosts,
  anonymous,
  filters,
  metric,
  onFilter,
  onShorten,
  query,
}: {
  allowedHosts?: string[]
  anonymous: boolean
  filters: Filter[]
  metric: DashboardMetric
  onFilter: (filter: Filter) => void
  onShorten: (days: number) => void
  query: StatsQuery
}) {
  const renderBreakdownCard = (card: BreakdownCardDefinition) => {
    const page = anonymous && card.title === 'Pages'
    const tabs = anonymous
      ? page ? [{ key: 'verified', label: 'Verified', dimension: 'page', metric: 'visitors' as const, metrics: ['visitors', 'visits', 'pageviews'] as StatsQuery['metrics'] }]
        : card.tabs.filter((tab) => isAnonymousDimensionAllowed(tab.dimension))
      : visibleTabs(card.tabs, allowedHosts)
    if (tabs.length === 0 || (page && filters.length > 0)) {
      return <section key={card.title} aria-label={page ? 'Verified pages' : card.title} className="rounded-xl border bg-card p-4" role="region">
        <h2 className="text-sm font-medium">{page ? 'Verified pages' : card.title}</h2>
        <p className="mt-3 text-sm text-muted-foreground">{page ? 'Verified page analytics require no filters. Remove active filters to view this partial dataset.' : 'Goals and event breakdowns are unavailable in anonymous mode because their labels may contain identifying data.'}</p>
      </section>
    }
    return <BreakdownCard
      key={card.title}
      title={page ? 'Verified pages' : anonymous && card.title === 'Sources' ? 'Channels' : card.title}
      tabs={tabs}
      query={page ? { ...query, includeImported: false } : query}
      onFilter={onFilter}
      allowSearch={!anonymous}
      allowFilter={!page}
      verifiedPaths={page}
      description={anonymous ? page ? 'Partial coverage: only verified page data under the current path policy. Historical unverified data and imports are excluded. Page filters and detailed searches are unavailable.' : 'Only canonical categories are available. Free-text labels, versions, and searches are restricted in anonymous mode.' : undefined}
    />
  }

  return <>
    <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
      {breakdownCards.slice(0, 4).map(renderBreakdownCard)}
    </div>
    <div className="mt-4 grid min-w-0 grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:grid-rows-[auto_auto]" data-testid="where-when-band">
      <div className="min-w-0 lg:col-start-1 lg:row-start-1" data-dashboard-band-item="where">
        <WhereCard query={query} metric={metric === 'pageviews' ? 'pageviews' : 'visitors'} onFilter={onFilter} />
      </div>
      <div className="min-w-0 lg:col-start-2 lg:row-span-2 lg:row-start-1" data-dashboard-band-item="weekly-rhythm">
        <WeeklyRhythmCard query={query} metric={metric === 'pageviews' ? 'pageviews' : 'visitors'} onShorten={onShorten} />
      </div>
      <div className="min-w-0 lg:col-start-1 lg:row-start-2" data-dashboard-band-item="goals-events">
        {breakdownCards.slice(4).map(renderBreakdownCard)}
      </div>
    </div>
  </>
}

type BreakdownCardDefinition = { title: string; tabs: BreakdownTab[] }

const breakdownCards: BreakdownCardDefinition[] = [
  { title: 'Pages', tabs: [
    { key: 'top', label: 'Top', dimension: 'page', metric: 'visitors', metrics: ['visitors', 'pageviews', 'time_on_page', 'exit_rate'] },
    { key: 'entry', label: 'Entry', dimension: 'entry', metric: 'visitors' },
    { key: 'exit', label: 'Exit', dimension: 'exit', metric: 'visitors' },
  ] },
  { title: 'Sources', tabs: [
    { key: 'source', label: 'Source', dimension: 'source', metric: 'visitors' },
    { key: 'channel', label: 'Channels', dimension: 'channel', metric: 'visitors' },
    { key: 'utm-source', label: 'UTM Source', dimension: 'utm_source', metric: 'visitors' },
    { key: 'medium', label: 'Medium', dimension: 'utm_medium', metric: 'visitors' },
    { key: 'campaign', label: 'Campaign', dimension: 'utm_campaign', metric: 'visitors' },
  ] },
  { title: 'Location', tabs: [
    { key: 'country', label: 'Countries', dimension: 'country', metric: 'visitors' },
    { key: 'region', label: 'Regions', dimension: 'region', metric: 'visitors' },
    { key: 'city', label: 'Cities', dimension: 'city', metric: 'visitors' },
    { key: 'language', label: 'Language', dimension: 'language', metric: 'visitors' },
    { key: 'hostname', label: 'Hostname', dimension: 'hostname', metric: 'visitors' },
  ] },
  { title: 'Environment', tabs: [
    { key: 'browser', label: 'Browser', dimension: 'browser', metric: 'visitors' },
    { key: 'browser-version', label: 'Browser version', dimension: 'browser_version', metric: 'visitors' },
    { key: 'os', label: 'OS', dimension: 'os', metric: 'visitors' },
    { key: 'os-version', label: 'OS version', dimension: 'os_version', metric: 'visitors' },
    { key: 'device', label: 'Device', dimension: 'device', metric: 'visitors' },
    { key: 'screen-size', label: 'Screen size', dimension: 'screen_size', metric: 'visitors' },
  ] },
  { title: 'Goals & events', tabs: [
    { key: 'goals', label: 'Goals', dimension: 'goal', metric: 'conversions' },
    { key: 'events', label: 'Events', dimension: 'event', metric: 'events' },
  ] },
]

function visibleTabs(tabs: BreakdownTab[], allowedHosts: string[] | undefined): BreakdownTab[] {
  const showHostname = (allowedHosts?.length ?? 0) > 1 || allowedHosts?.some((host) => host.startsWith('*.')) === true
  return showHostname ? tabs : tabs.filter((tab) => tab.dimension !== 'hostname')
}
