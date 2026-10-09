import { ReplayRecordingNotice } from '@/components/replay/ReplayRecordingNotice'
import { hasCapability, AccessDenied } from '@/lib/authorization'
import { createFileRoute } from '@tanstack/react-router'

import { ReplayPlayerPage } from '@/components/replay/ReplayPlayerPage'
import { useReplayAvailability } from '@/components/replay/api'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/replays/$id')({ component: ReplayRoute })

function ReplayRoute() {
  const { id, org, site: siteSlug } = Route.useParams()
  const { site, membership } = useSite(org, siteSlug)
  const allowed = hasCapability(membership, 'replay.read')
  const replay = useReplayAvailability(allowed ? site?.id : undefined)
  if (!site) return null
  if (!allowed) return <AccessDenied subject="watch replays" />
  return <div className="flex min-w-0 flex-col gap-4">
    <ReplayRecordingNotice settings={replay.data} pending={replay.isPending} error={replay.isError ? replay.error : null} onRetry={() => void replay.refetch()} org={org} site={siteSlug} />
    <ReplayPlayerPage sessionId={id} siteId={site.id} />
  </div>
}
