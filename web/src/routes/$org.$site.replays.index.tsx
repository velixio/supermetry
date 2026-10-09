import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { ReplaysPage } from '@/components/replay/ReplaysPage'
import { useReplayAvailability } from '@/components/replay/api'
import { dashboardSearch, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './$org'

const replaySearch = dashboardSearch.extend({
  cursor: z.string().optional(),
  replay: z.string().regex(/^[1-9][0-9]*$/).optional().catch(undefined),
})

export const Route = createFileRoute('/$org/$site/replays/')({
  validateSearch: (search) => replaySearch.parse(search),
  component: ReplaysRoute,
})

function ReplaysRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const membership = useMe().data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) => item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug)
  const replay = useReplayAvailability(site?.id)
  if (!site) return null
  if (!replay.enabled) return null
  const range = resolveRange(search.period, site.timezone || 'UTC', new Date(), search.from, search.to)
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex justify-end">
        <DatePicker labels={en.dashboard} search={search} onChange={(next) => void navigate({ search: { ...search, ...next, cursor: undefined } })} />
      </div>
      <ReplaysPage
        cursor={search.cursor}
        from={range.from}
        org={org}
        replayId={search.replay}
        site={siteSlug}
        siteId={site.id}
        to={range.to}
        onCursorChange={(cursor) => void navigate({ search: { ...search, cursor } })}
        onReplayChange={(replay, options) => void navigate({ search: { ...search, replay }, replace: options?.replace })}
      />
    </div>
  )
}
