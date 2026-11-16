import { createFileRoute } from '@tanstack/react-router'

import { AppInstallationPage } from '@/features/apps/AppInstallationPage'
import { InstallationPage } from '@/features/settings/SettingsPages'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/installation')({ component: InstallationRoute })

function InstallationRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  if (membership && site?.kind === 'app') {
    return <AppInstallationPage org={org} site={slug} orgId={membership.org.id} siteId={site.id} publicId={site.public_id ?? String(site.id)} timezone={site.timezone ?? 'UTC'} />
  }
  return membership && site ? (
    <InstallationPage
      orgId={membership.org.id}
      siteId={site.id}
      publicId={site.public_id ?? String(site.id)}
      isDemo={site.is_demo}
      dashboardHref={`/app/${org}/${slug}`}
      domainsHref={`/app/${org}/${slug}/settings/general`}
    />
  ) : null
}
