import { ReplayRecordingNotice } from '@/components/replay/ReplayRecordingNotice'
import { hasCapability, AccessDenied } from '@/lib/authorization'
import { createFileRoute } from '@tanstack/react-router'

import { DatePicker } from '@/components/DatePicker'
import { ReplaysPage } from '@/features/replays/ReplaysPage'
import { parseReplaySearch, readReplayLayout, rememberReplayLayout, replayFilterSearch, type ReplayLayout } from '@/features/replays/search'
import { useReplayAvailability } from '@/components/replay/api'
import { currentCursor, firstPage, nextPage, previousPage, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './app.$org'

export { parseReplaySearch }

export const Route = createFileRoute('/app/$org/$site/replays/')({
  validateSearch: parseReplaySearch,
  component: ReplaysRoute,
})

function ReplaysRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe().data
  const membership = me?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) => item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug)
  const allowed = hasCapability(membership, 'replay.read')
  const replay = useReplayAvailability(allowed ? site?.id : undefined)
  if (!site) return null
  if (!allowed) return <AccessDenied subject="watch replays" />
  const range = resolveRange(search.period, site.timezone || 'UTC', new Date(), search.from, search.to)
  const layout = search.layout ?? readReplayLayout(me?.user?.id ?? 0)
  const changeLayout = (next: ReplayLayout) => {
    if (me?.user?.id) rememberReplayLayout(me.user.id, next)
    void navigate({ search: { ...search, layout: next }, replace: true })
  }
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <ReplayRecordingNotice settings={replay.data} pending={replay.isPending} error={replay.isError ? replay.error : null} onRetry={() => void replay.refetch()} org={org} site={siteSlug} />
      <div className="flex justify-end">
        <DatePicker labels={en.dashboard} search={search} onChange={(next) => void navigate({ search: firstPage({ ...search, ...next }) })} />
      </div>
      <ReplaysPage
        cursor={currentCursor(search)}
        filters={{
          q: search.q,
          min_duration: search.min_duration,
          max_duration: search.max_duration,
          has_events: search.has_events,
          saved: search.saved,
        }}
        from={range.from}
        layout={layout}
        org={org}
        replayId={search.replay}
        site={siteSlug}
        siteId={site.id}
        to={range.to}
        page={search.page ?? 1}
        onFirst={() => void navigate({ search: firstPage(search) })}
        onFiltersChange={(patch) => void navigate({ search: replayFilterSearch(search, patch) })}
        onLayoutChange={changeLayout}
        onNext={(cursor) => void navigate({ search: nextPage(search, cursor) })}
        onPrevious={() => void navigate({ search: previousPage(search) })}
        onReplayChange={(replay, options) => void navigate({ search: { ...search, replay }, replace: options?.replace })}
      />
    </div>
  )
}
