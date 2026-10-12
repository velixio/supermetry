import { isAnonymousFilterAllowed, isAnonymousTracking, trackingPolicyKnown } from '@/lib/tracking-policy'
import type { Me } from '@/lib/me'
import { autoInterval } from '@/lib/chart'
import { queryClient } from '@/lib/queryClient'
import { dashboardSearch, parseFilters, resolveRange } from '@/lib/search'
import { timeseriesQueryOptions, type StatsQuery } from '@/lib/stats'

export async function prefetchDashboard(org: string, siteSlug: string, search: ReturnType<typeof dashboardSearch.parse>) {
  const me = queryClient.getQueryData<Me>(['me'])
  const membership = me?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) => item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug)
  if (!site || !trackingPolicyKnown(site)) return
  const { filters } = parseFilters(search.f)
  const anonymous = isAnonymousTracking(site)
  if (anonymous && filters.some((filter) => !isAnonymousFilterAllowed(filter))) return
  const timezone = site.timezone || 'UTC'
  const query: StatsQuery = {
    siteId: site.id,
    trackingMode: anonymous ? 'anonymous' : 'identified',
    dateRange: resolveRange(search.period, timezone, new Date(), search.from, search.to),
    metrics: ['visitors'],
    filters,
    compare: search.compare,
    timezone,
    live: search.live,
    includeImported: search.include_imported,
  }
  const metric = search.metric === 'exit_rate' || search.metric === 'time_on_page' ? 'visitors' : search.metric
  await queryClient.prefetchQuery(timeseriesQueryOptions(
    { ...query, metrics: [metric], dimensions: undefined, compare: search.compare === 'previous_period' ? search.compare : undefined },
    autoInterval(query.dateRange.from, query.dateRange.to),
  ))
}
