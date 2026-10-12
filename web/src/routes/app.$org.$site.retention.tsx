import { createFileRoute } from '@tanstack/react-router'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { RetentionPage } from '@/features/retention/RetentionPage'
import { countryName } from '@/lib/country'
import { parseDashboardSearch, parseFilters, removeFilter, replaceFilters, resolveRange, searchEnum } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './app.$org'

function parseRetentionSearch(value: unknown) {
  const raw = value && typeof value === 'object' ? value as Record<string, unknown> : {}
  return {
    ...parseDashboardSearch(raw),
    interval: searchEnum(raw.interval, ['day', 'week', 'month'] as const, 'day'),
    identity: searchEnum(raw.identity, ['anonymous', 'profile'] as const, 'anonymous'),
  }
}

export const Route = createFileRoute('/app/$org/$site/retention')({
  validateSearch: parseRetentionSearch,
  component: RetentionRoute,
})

function RetentionRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  if (!site) return null

  const { filters, dropped } = parseFilters(search.f)
  const timezone = site.timezone || 'UTC'

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <FilterBar
          dropped={dropped}
          filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
          filters={filters}
          invalidNotice={en.dashboard.invalidFilter}
          removeLabel={(filter) => `Remove ${filter.dimension} filter ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
          onRemove={(index) => void navigate({ search: { ...search, ...removeFilter(search, index) } })}
          siteId={site.id}
          canEdit={membership?.role !== 'viewer'}
          onApply={(segmentFilters) => void navigate({ search: { ...search, ...replaceFilters(search, segmentFilters) } })}
        />
        <DatePicker
          labels={en.dashboard}
          search={search}
          onChange={(next) => void navigate({ search: { ...search, ...next } })}
        />
      </div>
      <RetentionPage
        dateRange={resolveRange(search.period, timezone, new Date(), search.from, search.to)}
        filters={filters}
        identity={site.profiles_enabled ? search.identity : 'anonymous'}
        interval={search.interval}
        profilesEnabled={Boolean(site.profiles_enabled)}
        siteId={site.id}
        onIdentityChange={(identity) => void navigate({ search: { ...search, identity } })}
        onIntervalChange={(interval) => void navigate({ search: { ...search, interval } })}
      />
    </div>
  )
}
