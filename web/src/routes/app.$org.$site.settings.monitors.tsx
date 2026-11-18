import { createFileRoute } from '@tanstack/react-router'

import { MonitorsPage } from '@/features/apps/alerts/MonitorsPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/monitors')({ component: MonitorsRoute })

function MonitorsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  const canEdit = membership?.role === 'owner' || membership?.role === 'admin' || membership?.role === 'editor'
  return membership && site ? <MonitorsPage siteId={site.id} canEdit={canEdit} /> : null
}
