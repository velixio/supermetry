import { createFileRoute } from '@tanstack/react-router'

import { InstallationPage } from '@/features/settings/SettingsPages'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/installation')({ component: InstallationRoute })

function InstallationRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  return membership && site ? (
    <InstallationPage
      orgId={membership.org.id}
      siteId={site.id}
      publicId={site.public_id ?? String(site.id)}
    />
  ) : null
}
