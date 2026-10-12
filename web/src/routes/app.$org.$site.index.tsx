import { isAnonymousTracking, isAnonymousFilterAllowed, trackingPolicyKnown } from '@/lib/tracking-policy'
import { createFileRoute } from '@tanstack/react-router'
import { lazy, Suspense, useEffect, useState } from 'react'

import { DashboardChart } from '../components/DashboardChart'
import { DeferredContent } from '../components/DeferredContent'
import { DatePicker } from '../components/DatePicker'
import { DashboardExportMenu } from '../components/DashboardExportMenu'
import { FilterBar } from '../components/FilterBar'
import { DashboardNoDataBanner } from '../components/NoDataBanner'
import { StatsStrip } from '../components/StatsStrip'
import { ResponsiveFilters } from '../components/ResponsiveFilters'
import { Button } from '../components/ui/button'
import { Checkbox } from '../components/ui/checkbox'
import { Field, FieldLabel } from '../components/ui/field'
import { Skeleton } from '../components/ui/skeleton'
import { useShortcuts } from '../lib/shortcuts'
import { countryName } from '../lib/country'
import { fmt } from '../lib/format'
import { type StatsQuery } from '../lib/stats'
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
import type { Dashboard } from '../features/dashboards/api'
import { dashKey, parseDashKey } from '../features/dashboards/layout'
import type { TemplateKey } from '../features/dashboards/editor/model'
import { useMe } from './app.$org'

const loadDashboardBelowFold = () => import('@/components/DashboardBelowFold')
const DashboardBelowFold = (import.meta as ImportMeta & { env: { MODE: string } }).env.MODE === 'test'
  ? (await loadDashboardBelowFold()).DashboardBelowFold
  : lazy(() => loadDashboardBelowFold().then((module) => ({ default: module.DashboardBelowFold })))

const loadDashboardViewer = () => import('@/features/dashboards/DashboardViewer')
const loadDashboardSwitcher = () => import('@/features/dashboards/DashboardSwitcher')
const isTest = (import.meta as ImportMeta & { env: { MODE: string } }).env.MODE === 'test'
const DashboardViewer = isTest
  ? (await loadDashboardViewer()).DashboardViewer
  : lazy(() => loadDashboardViewer().then((module) => ({ default: module.DashboardViewer })))
const DashboardSwitcher = isTest
  ? (await loadDashboardSwitcher()).DashboardSwitcher
  : lazy(() => loadDashboardSwitcher().then((module) => ({ default: module.DashboardSwitcher })))

const loadDashboardEditor = () => import('@/features/dashboards/editor/DashboardEditor')
const loadDashboardManage = () => import('@/features/dashboards/DashboardManage')
const DashboardEditor = isTest
  ? (await loadDashboardEditor()).DashboardEditor
  : lazy(() => loadDashboardEditor().then((module) => ({ default: module.DashboardEditor })))
const DashboardManage = isTest
  ? (await loadDashboardManage()).DashboardManage
  : lazy(() => loadDashboardManage().then((module) => ({ default: module.DashboardManage })))

type EditState = { kind: 'edit'; id: number } | { kind: 'new'; template: TemplateKey } | null

export const Route = createFileRoute('/app/$org/$site/')({
  validateSearch: (search) => dashboardSearch.parse(search),
  loaderDeps: ({ search }) => search,
  loader: ({ deps, params }) => {
    void import('@/lib/dashboard-prefetch').then(({ prefetchDashboard }) => prefetchDashboard(params.org, params.site, deps))
  },
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
  const dashboardId = parseDashKey(search.dash)
  const [edit, setEdit] = useState<EditState>(null)
  // Moving to another dashboard (switcher, link, back button) ends edit mode unless the editor itself moved there.
  useEffect(() => {
    setEdit((current) => (current && !(current.kind === 'edit' && current.id === dashboardId) ? null : current))
  }, [dashboardId])

  useShortcuts({
    m: () => updateSearch({ ...search, period: '30d', from: undefined, to: undefined }),
    c: () =>
      updateSearch({
        ...search,
        compare: search.compare === 'off' ? 'previous_period' : 'off',
      }),
  })

  if (!site) return <><h1 className="sr-only">{en.navigation.dashboard}</h1><p className="sr-only">{en.loadingDashboard}</p></>

  if (!trackingPolicyKnown(site)) {
    return <><h1 className="sr-only">{en.navigation.dashboard}</h1><section className="rounded-xl border bg-card p-5 space-y-3" role="status">
      <h2 className="font-medium">Tracking policy is unavailable</h2>
      <p className="text-sm text-muted-foreground">Reload the policy before opening analytics. No dashboard data has been requested.</p>
      <Button variant="outline" onClick={() => void me.refetch()}>Reload tracking policy</Button>
    </section></>
  }
  const anonymous = isAnonymousTracking(site)
  if (anonymous && filters.some((filter) => !isAnonymousFilterAllowed(filter))) {
    return <><h1 className="sr-only">{en.navigation.dashboard}</h1><section className="rounded-xl border bg-card p-5 space-y-3" role="status">
      <h2 className="font-medium">These filters are unavailable in anonymous mode</h2>
      <p className="text-sm text-muted-foreground">The saved URL includes filters that could reveal identifying data. Clear them to view safe aggregate analytics.</p>
      <Button variant="outline" onClick={() => updateSearch(replaceFilters(search, filters.filter(isAnonymousFilterAllowed)))}>Clear unavailable filters</Button>
    </section></>
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
  const openDashboard = (key: string, dashboard?: Dashboard, replace = false) => {
    // A saved dashboard's own period and comparison are the starting point; the standard one keeps yours.
    void navigate({
      search: dashboard
        ? { ...search, dash: key, period: dashboard.layout.period ?? '30d', from: undefined, to: undefined, compare: dashboard.layout.compare ?? 'off' }
        : { ...search, dash: key },
      replace,
    })
  }
  const onFilter = (filter: Filter) => {
    if (!anonymous || isAnonymousFilterAllowed(filter)) void navigate({ search: addFilter(search, filter) })
  }
  const shortenToRawRange = (days: number) => {
    const end = new Date(`${query.dateRange.to}T12:00:00Z`)
    const start = new Date(end)
    start.setUTCDate(start.getUTCDate() - Math.max(0, days - 1))
    updateSearch({ ...search, period: 'custom', from: start.toISOString().slice(0, 10), to: query.dateRange.to })
  }

  return (
    <div className="flex min-w-0 flex-col gap-4" data-filter-count={filters.length}>
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">{en.navigation.dashboard}</h1>
        <p className="text-sm text-muted-foreground">{site.name}, {fmt.date(query.dateRange.from, timezone)} – {fmt.date(query.dateRange.to, timezone)}</p>
        <Suspense fallback={null}>
          <DashboardSwitcher
            siteId={site.id}
            value={search.dash}
            onChange={(key, dashboard) => openDashboard(key, dashboard)}
            onDefault={(key, dashboard) => openDashboard(key, dashboard, true)}
          />
        </Suspense>
        {membership?.role !== 'viewer' ? (
          <Suspense fallback={null}>
            <DashboardManage
              canWrite
              currentId={dashboardId}
              editing={edit !== null}
              onEdit={(id) => setEdit({ kind: 'edit', id })}
              onNew={(template) => setEdit({ kind: 'new', template })}
            />
          </Suspense>
        ) : null}
      </header>
      {edit ? null : <div className="flex flex-wrap items-start justify-between gap-3">
        <DashboardExportMenu query={query} anonymous={anonymous} />
        <ResponsiveFilters>
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
        <DatePicker
            search={search}
            labels={en.dashboard}
            onChange={updateSearch}
          />
        <Field orientation="horizontal" className="min-h-11 sm:min-h-9">
          <Checkbox id="dashboard-include-imported" checked={search.include_imported} onCheckedChange={(checked) => updateSearch({ ...search, include_imported: checked === true })} />
          <FieldLabel htmlFor="dashboard-include-imported">{en.polish.includeImported}</FieldLabel>
        </Field>
        </ResponsiveFilters>
      </div>}
      <DashboardNoDataBanner
        query={query}
        href={`/app/${org}/${siteSlug}/settings/installation`}
      />
      {edit ? (
        <Suspense fallback={<Skeleton className="h-64 w-full rounded-xl" />}>
          <DashboardEditor
            anonymous={anonymous}
            base={query}
            canWrite={membership?.role !== 'viewer'}
            mode={edit}
            orgId={membership?.org.id}
            siteId={site.id}
            timezone={timezone}
            onDeleted={() => { setEdit(null); openDashboard('builtin') }}
            onExit={() => setEdit(null)}
            onOpen={(dashboard) => { setEdit({ kind: 'edit', id: dashboard.id }); openDashboard(dashKey(dashboard.id), dashboard) }}
          />
        </Suspense>
      ) : dashboardId !== undefined ? (
        <Suspense fallback={<Skeleton className="h-64 w-full rounded-xl" />}>
          <DashboardViewer
            anonymous={anonymous}
            base={query}
            compare={search.compare}
            dashboardId={dashboardId}
            from={search.from}
            period={search.period}
            timezone={timezone}
            to={search.to}
            urlFilters={filters}
            onFilter={onFilter}
            onOpenStandard={() => openDashboard('builtin')}
            onShorten={shortenToRawRange}
          />
        </Suspense>
      ) : (
        <>
        <StatsStrip
          query={query}
          compare={search.compare}
          onSelectMetric={(metric) => updateSearch({ ...search, metric })}
        />
        <DashboardChart
          anonymous={anonymous}
          canEdit={membership?.role !== 'viewer'}
          compare={search.compare}
          metric={search.metric}
          query={query}
          siteId={site.id}
        />
        <DeferredContent className="min-h-[3000px] lg:min-h-[1600px]" label="Dashboard breakdowns">
          <Suspense fallback={<Skeleton className="size-full min-h-[inherit] rounded-xl" />}>
            <DashboardBelowFold
              allowedHosts={site.allowed_hosts}
              anonymous={anonymous}
              filters={filters}
              metric={search.metric}
              onFilter={onFilter}
              onShorten={shortenToRawRange}
              query={query}
            />
          </Suspense>
        </DeferredContent>
        </>
      )}
    </div>
  )
}
