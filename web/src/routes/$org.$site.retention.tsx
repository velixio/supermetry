import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { RetentionPage } from '@/features/retention/RetentionPage'
import { dashboardSearch, parseFilters, removeFilter, replaceFilters, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './$org'

const retentionSearch = dashboardSearch.extend({
  interval: z.enum(['day', 'week', 'month']).catch('day'),
})

export const Route = createFileRoute('/$org/$site/retention')({
  validateSearch: (search) => retentionSearch.parse(search),
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
          filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.join(', ')}`}
          filters={filters}
          invalidNotice={en.dashboard.invalidFilter}
          removeLabel={(filter) => `Remove ${filter.dimension} filter ${filter.values.join(', ')}`}
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
        interval={search.interval}
        siteId={site.id}
        onIntervalChange={(interval) => void navigate({ search: { ...search, interval } })}
      />
    </div>
  )
}
