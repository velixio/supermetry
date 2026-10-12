import { BreakdownCard, type BreakdownTab } from '@/components/BreakdownCard'
import { WeeklyRhythmCard } from '@/features/map/WeeklyRhythmCard'
import { WhereCard } from '@/features/map/WhereCard'
import type { InspectControl } from '@/lib/inspect'
import { isAnonymousDimensionAllowed } from '@/lib/tracking-policy'
import type { DashboardMetric, Filter } from '@/lib/search'
import type { StatsQuery } from '@/lib/stats'
import { ui } from '@/messages/ui'

export function DashboardBelowFold({
  allowedHosts,
  anonymous,
  filters,
  inspect,
  metric,
  onFilter,
  onShorten,
  query,
}: {
  allowedHosts?: string[]
  anonymous: boolean
  filters: Filter[]
  inspect?: InspectControl
  metric: DashboardMetric
  onFilter: (filter: Filter) => void
  onShorten: (days: number) => void
  query: StatsQuery
}) {
  const renderBreakdownCard = (card: BreakdownCardDefinition) => {
    const page = anonymous && card.id === 'pages'
    const tabs = anonymous
      ? page ? [{ key: 'verified', label: ui.breakdown.verified, dimension: 'page', metric: 'visitors' as const, metrics: ['visitors', 'visits', 'pageviews'] as StatsQuery['metrics'] }]
        : card.tabs.filter((tab) => isAnonymousDimensionAllowed(tab.dimension))
      : visibleTabs(card.tabs, allowedHosts)
    if (tabs.length === 0 || (page && filters.length > 0)) {
      return <section key={card.id} aria-label={page ? ui.breakdown.verifiedPages : card.title} className="rounded-xl border bg-card p-4" role="region">
        <h2 className="text-sm font-medium">{page ? ui.breakdown.verifiedPages : card.title}</h2>
        <p className="mt-3 text-sm text-muted-foreground">{page ? ui.breakdown.verifiedNone : ui.breakdown.anonymousGoals}</p>
      </section>
    }
    return <BreakdownCard
      key={card.id}
      title={page ? ui.breakdown.verifiedPages : anonymous && card.id === 'sources' ? ui.breakdown.channels : card.title}
      tabs={tabs}
      query={page ? { ...query, includeImported: false } : query}
      onFilter={onFilter}
      inspect={inspect}
      allowSearch={!anonymous}
      allowFilter={!page}
      verifiedPaths={page}
      description={anonymous ? page ? ui.breakdown.partialCoverage : ui.breakdown.canonicalOnly : undefined}
    />
  }

  return <>
    <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
      {breakdownCards().slice(0, 4).map(renderBreakdownCard)}
    </div>
    <div className="mt-4 grid min-w-0 grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:grid-rows-[auto_auto]" data-testid="where-when-band">
      <div className="min-w-0 lg:col-start-1 lg:row-start-1" data-dashboard-band-item="where">
        <WhereCard query={query} metric={metric === 'pageviews' ? 'pageviews' : 'visitors'} onFilter={onFilter} />
      </div>
      <div className="min-w-0 lg:col-start-2 lg:row-span-2 lg:row-start-1" data-dashboard-band-item="weekly-rhythm">
        <WeeklyRhythmCard query={query} metric={metric === 'pageviews' ? 'pageviews' : 'visitors'} onShorten={onShorten} />
      </div>
      <div className="min-w-0 lg:col-start-1 lg:row-start-2" data-dashboard-band-item="goals-events">
        {breakdownCards().slice(4).map(renderBreakdownCard)}
      </div>
    </div>
  </>
}

type BreakdownCardDefinition = { id: string; title: string; tabs: BreakdownTab[] }

// Built on each render so labels follow the active language.
function breakdownCards(): BreakdownCardDefinition[] {
  const t = ui.breakdown.tabs
  return [
  { id: 'pages', title: ui.breakdown.pages, tabs: [
    { key: 'top', label: t.top, dimension: 'page', metric: 'visitors', metrics: ['visitors', 'pageviews', 'time_on_page', 'exit_rate'] },
    { key: 'entry', label: t.entry, dimension: 'entry', metric: 'visitors' },
    { key: 'exit', label: t.exit, dimension: 'exit', metric: 'visitors' },
  ] },
  { id: 'sources', title: ui.breakdown.sources, tabs: [
    { key: 'source', label: t.source, dimension: 'source', metric: 'visitors' },
    { key: 'channel', label: t.channel, dimension: 'channel', metric: 'visitors' },
    { key: 'utm-source', label: t.utmSource, dimension: 'utm_source', metric: 'visitors' },
    { key: 'medium', label: t.medium, dimension: 'utm_medium', metric: 'visitors' },
    { key: 'campaign', label: t.campaign, dimension: 'utm_campaign', metric: 'visitors' },
  ] },
  { id: 'location', title: ui.breakdown.location, tabs: [
    { key: 'country', label: t.countries, dimension: 'country', metric: 'visitors' },
    { key: 'region', label: t.regions, dimension: 'region', metric: 'visitors' },
    { key: 'city', label: t.cities, dimension: 'city', metric: 'visitors' },
    { key: 'language', label: t.language, dimension: 'language', metric: 'visitors' },
    { key: 'hostname', label: t.hostname, dimension: 'hostname', metric: 'visitors' },
  ] },
  { id: 'environment', title: ui.breakdown.environment, tabs: [
    { key: 'browser', label: t.browser, dimension: 'browser', metric: 'visitors' },
    { key: 'browser-version', label: t.browserVersion, dimension: 'browser_version', metric: 'visitors' },
    { key: 'os', label: t.os, dimension: 'os', metric: 'visitors' },
    { key: 'os-version', label: t.osVersion, dimension: 'os_version', metric: 'visitors' },
    { key: 'device', label: t.device, dimension: 'device', metric: 'visitors' },
    { key: 'screen-size', label: t.screenSize, dimension: 'screen_size', metric: 'visitors' },
  ] },
  { id: 'goals', title: ui.breakdown.goalsEvents, tabs: [
    { key: 'goals', label: t.goals, dimension: 'goal', metric: 'conversions' },
    { key: 'events', label: t.events, dimension: 'event', metric: 'events' },
  ] },
  ]
}

function visibleTabs(tabs: BreakdownTab[], allowedHosts: string[] | undefined): BreakdownTab[] {
  const showHostname = (allowedHosts?.length ?? 0) > 1 || allowedHosts?.some((host) => host.startsWith('*.')) === true
  return showHostname ? tabs : tabs.filter((tab) => tab.dimension !== 'hostname')
}
