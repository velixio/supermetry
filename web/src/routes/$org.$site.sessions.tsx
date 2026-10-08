import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldLabel } from '@/components/ui/field'
import { SessionsPage } from '@/features/sessions/SessionsPage'
import { countryName } from '@/lib/country'
import { dashboardSearch, parseFilters, removeFilter, replaceFilters, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './$org'

const sessionsSearch = dashboardSearch.extend({
  cursor: z.string().optional(),
  session: z.string().regex(/^[1-9][0-9]*$/).optional().catch(undefined),
  replay: z.boolean().optional().catch(false),
})

export const Route = createFileRoute('/$org/$site/sessions')({
  validateSearch: (search) => sessionsSearch.parse(search),
  component: SessionsRoute,
})

function SessionsRoute() {
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
          onRemove={(index) => void navigate({ search: { ...search, ...removeFilter(search, index), cursor: undefined } })}
          siteId={site.id}
          canEdit={membership?.role !== 'viewer'}
          onApply={(segmentFilters) => void navigate({ search: { ...search, ...replaceFilters(search, segmentFilters), cursor: undefined } })}
        />
        <DatePicker
          labels={en.dashboard}
          search={search}
          onChange={(next) => void navigate({ search: { ...search, ...next, cursor: undefined } })}
        />
      </div>
      <Field orientation="horizontal">
        <Checkbox id="sessions-has-replay" checked={search.replay ?? false} onCheckedChange={(checked) => void navigate({ search: { ...search, replay: checked === true, cursor: undefined } })} />
        <FieldLabel htmlFor="sessions-has-replay">Has replay</FieldLabel>
      </Field>
      <SessionsPage
        cursor={search.cursor}
        dateRange={resolveRange(search.period, timezone, new Date(), search.from, search.to)}
        filters={filters}
        hasReplayOnly={search.replay}
        org={org}
        onCursorChange={(cursor) => void navigate({ search: { ...search, cursor } })}
        onSelect={(session) => void navigate({ search: { ...search, session } })}
        selectedSessionId={search.session}
        siteId={site.id}
        site={siteSlug}
      />
    </div>
  )
}
