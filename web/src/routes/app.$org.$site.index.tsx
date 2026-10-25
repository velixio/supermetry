import { isAnonymousTracking, isAnonymousFilterAllowed, trackingPolicyKnown } from '@/lib/tracking-policy'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { lazy, Suspense, useEffect, useRef, useState, type CSSProperties } from 'react'

import { DashboardChart } from '../components/DashboardChart'
import { rememberInterval, rememberedInterval } from '@/lib/chart'
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
import { activeInspectTarget, inspectAllowed, nextInspect, parseInspectSearch, serializeInspect, withoutInspect, type InspectControl, type InspectTarget } from '../lib/inspect'
import { DashboardPanels } from '@/components/DashboardPanels'
import { Inspector, inspectorDimensionLabel, inspectorTitle } from '@/components/Inspector'
import { en } from '../messages/en'
import { createAuthConfigApi } from '@/features/auth/api'
import { PixelDashboard } from '@/features/pixels/PixelDashboard'
import { getSite } from '@/features/sites/api'
import type { Site } from '@/lib/me'
import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
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
  validateSearch: (search) => ({ ...dashboardSearch.parse(search), ...parseInspectSearch(search) }),
  // Opening or closing the inspector must not re-run the dashboard prefetch.
  loaderDeps: ({ search }) => withoutInspect(search),
  loader: ({ deps, params }) => {
    void import('@/lib/dashboard-prefetch').then(({ prefetchDashboard }) => prefetchDashboard(params.org, params.site, deps))
  },
  component: DashboardStateRoute,
})

function DashboardStateRoute() {
  const { org, site: requestedSite } = Route.useParams()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) => item.slug === requestedSite || item.public_id === requestedSite || String(item.id) === requestedSite)
  const siteDetails = useQuery({
    queryKey: ['site', site?.id],
    queryFn: () => getSite(site!.id, membership!.org.id),
    enabled: Boolean(site && membership && site.kind !== 'web' && site.kind !== 'pixel'),
  })
  const resolvedSite = siteDetails.data?.id === site?.id ? siteDetails.data : site
  if (site && site.kind !== 'web' && site.kind !== 'pixel' && siteDetails.isPending) return <PageContainer><PageHeader title={en.navigation.dashboard} /><p className="sr-only">{en.loadingDashboard}</p></PageContainer>
  if (resolvedSite?.kind === 'pixel') return <PixelDashboardRoute site={resolvedSite} />
  return <WebDashboardStateRoute />
}

function PixelDashboardRoute({ site }: { site: Site }) {
  const config = useQuery({ queryKey: ['auth-config'], queryFn: createAuthConfigApi(), staleTime: 300_000 })
  return <PixelDashboard site={site} homepage={config.data?.homepage_url ?? 'https://supermetry.com'} />
}

function WebDashboardStateRoute() {
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
  // The control that opened the inspector; focus returns to it on close.
  const inspectorOrigin = useRef<HTMLElement | null>(null)
  // The sticky filter row wraps (notice, chips); the sticky inspector clears its real height via --dashboard-filter-h.
  const [filterRow, setFilterRow] = useState<HTMLDivElement | null>(null)
  const [filterRowHeight, setFilterRowHeight] = useState<number | undefined>(undefined)
  useEffect(() => {
    if (!filterRow || typeof ResizeObserver === 'undefined') {
      setFilterRowHeight(undefined)
      return
    }
    const observer = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1]
      const height = entry?.borderBoxSize?.[0]?.blockSize ?? (entry?.target as HTMLElement | undefined)?.offsetHeight
      if (height) setFilterRowHeight(height)
    })
    observer.observe(filterRow)
    return () => observer.disconnect()
  }, [filterRow])
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

  if (!site) return <PageContainer><PageHeader title={en.navigation.dashboard} /><p className="sr-only">{en.loadingDashboard}</p></PageContainer>

  if (!trackingPolicyKnown(site)) {
    return <PageContainer><PageHeader title={en.navigation.dashboard} /><section className="rounded-xl border bg-card p-5 space-y-3" role="status">
      <h2 className="font-medium">Tracking policy is unavailable</h2>
      <p className="text-sm text-muted-foreground">Reload the policy before opening analytics. No dashboard data has been requested.</p>
      <Button variant="outline" onClick={() => void me.refetch()}>Reload tracking policy</Button>
    </section></PageContainer>
  }
  const anonymous = isAnonymousTracking(site)
  if (anonymous && filters.some((filter) => !isAnonymousFilterAllowed(filter))) {
    return <PageContainer><PageHeader title={en.navigation.dashboard} /><section className="rounded-xl border bg-card p-5 space-y-3" role="status">
      <h2 className="font-medium">These filters are unavailable in anonymous mode</h2>
      <p className="text-sm text-muted-foreground">The saved URL includes filters that could reveal identifying data. Clear them to view safe aggregate analytics.</p>
      <Button variant="outline" onClick={() => updateSearch(replaceFilters(search, filters.filter(isAnonymousFilterAllowed)))}>Clear unavailable filters</Button>
    </section></PageContainer>
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
  const standardDashboard = edit === null && dashboardId === undefined
  // An ignored or blocked `inspect` value intentionally stays in the URL and reappears on the standard dashboard.
  const inspectTarget = activeInspectTarget(search.inspect, { anonymous, standardDashboard })
  // History policy: opening or switching pushes, so Back closes the inspector; closing replaces, so Back never reopens it.
  const closeInspector = () => {
    void navigate({ search: { ...search, inspect: undefined }, replace: true })
  }
  // Same value again toggles it closed. Used by the Inspect controls.
  const openInspector = (target: InspectTarget) => {
    const next = nextInspect(inspectTarget, target)
    if (next === undefined) closeInspector()
    else void navigate({ search: { ...search, inspect: next } })
  }
  const trafficMetric = search.metric === 'pageviews' ? 'pageviews' : 'visitors'
  const inspectControl: InspectControl | undefined = standardDashboard ? {
    selected: inspectTarget,
    can: (target) => inspectAllowed(target, anonymous),
    open: (target, origin) => {
      inspectorOrigin.current = origin
      openInspector(target)
    },
  } : undefined

  return (
    <PageContainer>
      <div
        className="contents"
        data-filter-count={filters.length}
        style={filterRowHeight === undefined ? undefined : ({ '--dashboard-filter-h': `${filterRowHeight}px` } as CSSProperties)}
      >
      <PageHeader
        title={en.navigation.dashboard}
        description={`${fmt.date(query.dateRange.from, timezone)} – ${fmt.date(query.dateRange.to, timezone)}`}
        actions={
          <>
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
            {edit ? null : <DashboardExportMenu query={query} anonymous={anonymous} />}
          </>
        }
      />
      {edit ? null : (
        <div ref={setFilterRow} aria-label={en.polish.filters} className="sticky top-11 z-10 bg-background py-2" role="group">
          <ResponsiveFilters className="justify-start">
            <DatePicker
              search={search}
              labels={en.dashboard}
              onChange={updateSearch}
            />
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
            <Field orientation="horizontal" className="w-auto min-h-9 gap-2">
              <Checkbox id="dashboard-include-imported" checked={search.include_imported} onCheckedChange={(checked) => updateSearch({ ...search, include_imported: checked === true })} />
              <FieldLabel htmlFor="dashboard-include-imported" className="text-sm text-muted-foreground">{en.polish.includeImported}</FieldLabel>
            </Field>
          </ResponsiveFilters>
        </div>
      )}
      <DashboardNoDataBanner
        siteId={site.id}
        orgId={membership?.org.id}
        enabled={!site.is_demo}
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
        <DashboardPanels
          returnFocusRef={inspectorOrigin}
          inspector={inspectTarget ? {
            key: serializeInspect(inspectTarget),
            title: inspectorTitle(inspectTarget),
            dimension: inspectorDimensionLabel(inspectTarget.dimension),
            onClose: closeInspector,
            body: (
              <Inspector
                key={serializeInspect(inspectTarget)}
                anonymous={anonymous}
                query={query}
                target={inspectTarget}
                trafficMetric={trafficMetric}
                onFilter={onFilter}
              />
            ),
          } : undefined}
          main={
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
                interval={search.granularity ?? rememberedInterval(site.id)}
                onIntervalChange={(granularity) => {
                  rememberInterval(site.id, granularity)
                  updateSearch({ ...search, granularity })
                }}
              />
              <DeferredContent className="min-h-[3000px] lg:min-h-[1600px]" label="Dashboard breakdowns">
                <Suspense fallback={<Skeleton className="size-full min-h-[inherit] rounded-xl" />}>
                  <DashboardBelowFold
                    allowedHosts={site.allowed_hosts}
                    anonymous={anonymous}
                    filters={filters}
                    inspect={inspectControl}
                    metric={search.metric}
                    onFilter={onFilter}
                    onShorten={shortenToRawRange}
                    query={query}
                  />
                </Suspense>
              </DeferredContent>
            </>
          }
        />
      )}
      </div>
    </PageContainer>
  )
}
