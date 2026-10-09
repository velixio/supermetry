import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { EventsPage } from '@/features/events/EventsPage'
import { countryName } from '@/lib/country'
import {
  addFilter,
  dashboardSearch,
  parseFilters,
  removeFilter,
  replaceFilters,
  resolveRange,
} from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './$org'

const eventsSearch = dashboardSearch.extend({
  name: z.string().catch(''),
  property: z.string().catch(''),
  cursor: z.string().optional(),
  replay: z.string().regex(/^[1-9][0-9]*$/).optional().catch(undefined),
})

export const Route = createFileRoute('/$org/$site/events')({
  validateSearch: (search) => eventsSearch.parse(search),
  component: EventsRoute,
})

function EventsRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  if (!membership || !site) return null

  const { filters, dropped } = parseFilters(search.f)
  const dateRange = resolveRange(search.period, site.timezone || 'UTC', new Date(), search.from, search.to)

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <FilterBar
          canEdit={membership.role !== 'viewer'}
          dateRange={dateRange}
          dropped={dropped}
          filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
          filters={filters}
          invalidNotice={en.dashboard.invalidFilter}
          orgId={membership.org.id}
          removeLabel={(filter) => `Remove ${filter.dimension} filter ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
          siteId={site.id}
          onAdd={(filter) => void navigate({ search: { ...search, f: addFilter(search, filter).f, cursor: undefined } })}
          onApply={(next) => void navigate({ search: { ...search, f: replaceFilters(search, next).f, cursor: undefined } })}
          onRemove={(index) => void navigate({ search: { ...search, f: removeFilter(search, index).f, cursor: undefined } })}
        />
        <DatePicker
          labels={en.dashboard}
          search={search}
          onChange={(next) => void navigate({ search: { ...search, ...next, cursor: undefined } })}
        />
      </div>
      <EventsPage
        cursor={search.cursor}
        dateRange={dateRange}
        filters={filters}
        name={search.name}
        orgId={membership.org.id}
        org={org}
        replayId={search.replay}
        propertyKey={search.property}
        siteId={site.id}
        site={siteSlug}
        onNameChange={(name) => void navigate({ search: { ...search, name, cursor: undefined } })}
        onNext={(cursor) => void navigate({ search: { ...search, cursor } })}
        onPrevious={() => void navigate({ search: { ...search, cursor: undefined } })}
        onPropertyChange={(property) => void navigate({ search: { ...search, property } })}
        onReplayChange={(replay, options) => void navigate({ search: { ...search, replay }, replace: options?.replace })}
      />
    </div>
  )
}
