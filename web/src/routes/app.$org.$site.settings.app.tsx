import { createFileRoute } from '@tanstack/react-router'

import { AppSettingsPage } from '@/features/settings/AppSettingsPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/app')({ component: AppSettingsRoute })

function AppSettingsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  if (!membership || !site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return <AppSettingsPage orgId={membership.org.id} siteId={site.id} publicId={site.public_id ?? String(site.id)} />
}
