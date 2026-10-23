import { createFileRoute } from '@tanstack/react-router'

import { StarsOverview } from '@/features/github-stars/StarsOverview'

export const Route = createFileRoute('/app/$org/github-stars/')({ component: OverviewRoute })

function OverviewRoute() {
  const { org } = Route.useParams()
  return <StarsOverview org={org} />
}
