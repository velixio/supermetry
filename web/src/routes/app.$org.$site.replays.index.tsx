import { ReplayRecordingNotice } from '@/components/replay/ReplayRecordingNotice'
import { hasCapability, AccessDenied } from '@/lib/authorization'
import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { DatePicker } from '@/components/DatePicker'
import { ReplaysPage } from '@/components/replay/ReplaysPage'
import { useReplayAvailability } from '@/components/replay/api'
import { currentCursor, dashboardSearch, firstPage, nextPage, paginationSearch, previousPage, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './app.$org'

const replaySearch = dashboardSearch.extend({
  ...paginationSearch,
  replay: z.string().regex(/^[1-9][0-9]*$/).optional().catch(undefined),
})

export const Route = createFileRoute('/app/$org/$site/replays/')({
  validateSearch: (search) => replaySearch.parse(search),
  component: ReplaysRoute,
})

function ReplaysRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const membership = useMe().data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) => item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug)
  const allowed = hasCapability(membership, 'replay.read')
  const replay = useReplayAvailability(allowed ? site?.id : undefined)
  if (!site) return null
  if (!allowed) return <AccessDenied subject="watch replays" />
  const range = resolveRange(search.period, site.timezone || 'UTC', new Date(), search.from, search.to)
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <ReplayRecordingNotice settings={replay.data} pending={replay.isPending} error={replay.isError ? replay.error : null} onRetry={() => void replay.refetch()} org={org} site={siteSlug} />
      <div className="flex justify-end">
        <DatePicker labels={en.dashboard} search={search} onChange={(next) => void navigate({ search: firstPage({ ...search, ...next }) })} />
      </div>
      <ReplaysPage
        cursor={currentCursor(search)}
        from={range.from}
        org={org}
        replayId={search.replay}
        site={siteSlug}
        siteId={site.id}
        to={range.to}
        page={search.page ?? 1}
        onFirst={() => void navigate({ search: firstPage(search) })}
        onNext={(cursor) => void navigate({ search: nextPage(search, cursor) })}
        onPrevious={() => void navigate({ search: previousPage(search) })}
        onReplayChange={(replay, options) => void navigate({ search: { ...search, replay }, replace: options?.replace })}
      />
    </div>
  )
}
