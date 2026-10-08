import { createFileRoute } from '@tanstack/react-router'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { VitalsPage } from '@/features/vitals/VitalsPage'
import {
  dashboardSearch,
  parseFilters,
  removeFilter,
  replaceFilters,
  resolveRange,
} from '@/lib/search'
import { en } from '@/messages/en'
import { countryName } from '@/lib/country'

import { useMe } from './$org'

export const Route = createFileRoute('/$org/$site/vitals')({
  validateSearch: (search) => dashboardSearch.parse(search),
  component: VitalsRoute,
})

function VitalsRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const { filters, dropped } = parseFilters(search.f)
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find(
    (item) => item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  const updateSearch = (next: typeof search) => void navigate({ search: next })

  if (!site) return <p className="sr-only">{en.loadingDashboard}</p>

  return (
    <div className="flex min-w-0 flex-col gap-4" data-filter-count={filters.length}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <FilterBar
          siteId={site.id}
          canEdit={membership?.role !== 'viewer'}
          filters={filters}
          dropped={dropped}
          invalidNotice={en.dashboard.invalidFilter}
          filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
          removeLabel={(filter) => en.dashboard.filterLabel
            .replace('{dimension}', filter.dimension)
            .replace('{value}', filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', '))}
          onApply={(nextFilters) => updateSearch(replaceFilters(search, nextFilters))}
          onRemove={(index) => updateSearch(removeFilter(search, index))}
        />
        <DatePicker search={search} labels={en.dashboard} onChange={updateSearch} />
      </div>
      <VitalsPage
        siteId={site.id}
        dateRange={resolveRange(
          search.period,
          site.timezone || 'UTC',
          new Date(),
          search.from,
          search.to,
        )}
        filters={filters}
      />
    </div>
  )
}
