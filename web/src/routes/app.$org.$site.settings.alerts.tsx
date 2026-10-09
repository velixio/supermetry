import { createFileRoute } from '@tanstack/react-router'

import { AlertsPage } from '@/features/settings/AlertsPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/alerts')({ component: AlertsRoute })

function AlertsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  return membership && site ? <AlertsPage orgId={membership.org.id} siteId={site.id} /> : null
}
