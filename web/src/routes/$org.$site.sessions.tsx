import { hasCapability } from '@/lib/authorization'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { Checkbox } from '@/components/ui/checkbox'
import { Field, FieldLabel } from '@/components/ui/field'
import { SessionsPage } from '@/features/sessions/SessionsPage'
import { countryName } from '@/lib/country'
import { currentCursor, dashboardSearch, firstPage, nextPage, paginationSearch, parseFilters, previousPage, removeFilter, replaceFilters, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './$org'

const sessionsSearch = dashboardSearch.extend({
  ...paginationSearch,
  session: z.string().regex(/^[1-9][0-9]*$/).optional().catch(undefined),
  has_replay: z.boolean().optional().catch(false),
  replay: z.string().regex(/^[1-9][0-9]*$/).optional().catch(undefined),
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
  const canReplay = hasCapability(membership, 'replay.read')
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
          onRemove={(index) => void navigate({ search: firstPage({ ...search, ...removeFilter(search, index) }) })}
          siteId={site.id}
          canEdit={membership?.role !== 'viewer'}
          onApply={(segmentFilters) => void navigate({ search: firstPage({ ...search, ...replaceFilters(search, segmentFilters) }) })}
        />
        <DatePicker
          labels={en.dashboard}
          search={search}
          onChange={(next) => void navigate({ search: firstPage({ ...search, ...next }) })}
        />
      </div>
      {canReplay ? <Field orientation="horizontal">
        <Checkbox id="sessions-has-replay" checked={search.has_replay ?? false} onCheckedChange={(checked) => void navigate({ search: firstPage({ ...search, has_replay: checked === true }) })} />
        <FieldLabel htmlFor="sessions-has-replay">Has replay</FieldLabel>
      </Field> : null}
      <SessionsPage
        cursor={currentCursor(search)}
        dateRange={resolveRange(search.period, timezone, new Date(), search.from, search.to)}
        filters={filters}
        hasReplayOnly={canReplay && search.has_replay}
        org={org}
        page={search.page ?? 1}
        replayId={search.replay}
        onFirst={() => void navigate({ search: firstPage(search) })}
        onNext={(cursor) => void navigate({ search: nextPage(search, cursor) })}
        onPrevious={() => void navigate({ search: previousPage(search) })}
        onReplayChange={(replay, options) => void navigate({ search: { ...search, replay }, replace: options?.replace })}
        onSelect={(session) => void navigate({ search: { ...search, session } })}
        selectedSessionId={search.session}
        siteId={site.id}
        site={siteSlug}
      />
    </div>
  )
}
