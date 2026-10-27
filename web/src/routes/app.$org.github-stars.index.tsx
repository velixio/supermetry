import { createFileRoute } from '@tanstack/react-router'

import { canManageStars } from '@/features/github-stars/starsOrg'
import { StarsOverview } from '@/features/github-stars/StarsOverview'
import { useOrganization } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/github-stars/')({ component: OverviewRoute })

function OverviewRoute() {
  const { org } = Route.useParams()
  const membership = useOrganization(org)
  if (!membership) return null
  return <StarsOverview org={org} orgId={membership.org.id} canManage={canManageStars(membership.role)} />
}
