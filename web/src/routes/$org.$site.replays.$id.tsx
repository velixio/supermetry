import { hasCapability, AccessDenied } from '@/lib/authorization'
import { createFileRoute } from '@tanstack/react-router'

import { ReplayPlayerPage } from '@/components/replay/ReplayPlayerPage'
import { useReplayAvailability } from '@/components/replay/api'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/$site/replays/$id')({ component: ReplayRoute })

function ReplayRoute() {
  const { id, org, site: siteSlug } = Route.useParams()
  const { site, membership } = useSite(org, siteSlug)
  const allowed = hasCapability(membership, 'replay.read')
  const replay = useReplayAvailability(allowed ? site?.id : undefined)
  if (!site) return null
  if (!allowed) return <AccessDenied subject="watch replays" />
  if (!replay.enabled) return null
  return <ReplayPlayerPage sessionId={id} siteId={site.id} />
}
