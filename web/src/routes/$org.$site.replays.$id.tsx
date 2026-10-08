import { createFileRoute } from '@tanstack/react-router'

import { ReplayPlayerPage } from '@/components/replay/ReplayPlayerPage'
import { useReplayAvailability } from '@/components/replay/api'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/$site/replays/$id')({ component: ReplayRoute })

function ReplayRoute() {
  const { id, org, site: siteSlug } = Route.useParams()
  const { site } = useSite(org, siteSlug)
  const replay = useReplayAvailability(site?.id)
  if (!site) return null
  if (!replay.enabled) return null
  return <ReplayPlayerPage sessionId={id} siteId={site.id} />
}
