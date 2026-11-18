import { createFileRoute } from '@tanstack/react-router'

import { AppAlertsPage } from '@/features/apps/alerts/AppAlertsPage'
import { AlertsPage } from '@/features/settings/AlertsPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/alerts')({ component: AlertsRoute })

function AlertsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  if (membership && site?.kind === 'app') return <AppAlertsPage orgId={membership.org.id} orgSlug={org} siteId={site.id} siteSlug={slug} />
  return membership && site ? <AlertsPage orgId={membership.org.id} orgSlug={org} siteId={site.id} /> : null
}
