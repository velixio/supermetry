import { createFileRoute } from '@tanstack/react-router'

import { ExclusionsPage } from '@/features/settings/ExclusionsPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/$site/settings/tracking')({ component: ExclusionsRoute })

function ExclusionsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  return membership && site ? (
	<ExclusionsPage
		orgId={membership.org.id}
		siteId={site.id}
		canManageHeatmaps={membership.role === 'owner' || membership.role === 'admin'}
	/>
  ) : null
}
