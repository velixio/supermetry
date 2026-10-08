import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { JourneysPage } from '@/features/journeys/JourneysPage'
import { countryName } from '@/lib/country'
import { dashboardSearch, parseFilters, removeFilter, replaceFilters, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './$org'

const journeysSearch = dashboardSearch.extend({
  direction: z.enum(['start', 'end']).catch('start'),
  path: z.string().catch(''),
  steps: z.coerce.number().int().min(2).max(5).catch(3),
})

export const Route = createFileRoute('/$org/$site/journeys')({
  validateSearch: (search) => journeysSearch.parse(search),
  component: JourneysRoute,
})

function JourneysRoute() {
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
  const updateDashboardSearch = (next: typeof search) => void navigate({ search: next })
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
          onChange={(next) => updateDashboardSearch({ ...search, ...next })}
        />
      </div>
      <JourneysPage
        dateRange={resolveRange(search.period, timezone, new Date(), search.from, search.to)}
        direction={search.direction}
        filters={filters}
        path={search.path}
        siteId={site.id}
        steps={search.steps}
        onControlsChange={(controls) => void navigate({ search: { ...search, ...controls } })}
      />
    </div>
  )
}
