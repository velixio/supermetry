import { createFileRoute } from '@tanstack/react-router'

import { SilencesPage } from '@/features/apps/alerts/SilencesPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/alert-silences')({ component: SilencesRoute })

function SilencesRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  const canEdit = membership?.role === 'owner' || membership?.role === 'admin' || membership?.role === 'editor'
  return membership && site ? <SilencesPage siteId={site.id} orgId={membership.org.id} siteTimezone={site.timezone ?? 'UTC'} canEdit={canEdit} /> : null
}
