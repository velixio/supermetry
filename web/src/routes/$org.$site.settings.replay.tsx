import { createFileRoute } from '@tanstack/react-router'

import { ReplaySettingsPage } from '@/components/replay/ReplaySettingsPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/$site/settings/replay')({ component: ReplaySettingsRoute })

function ReplaySettingsRoute() {
  const { org, site: siteSlug } = Route.useParams()
  const { membership, site } = useSite(org, siteSlug)
  if (!membership || !site) return null
  return <ReplaySettingsPage editable={membership.role === 'owner' || membership.role === 'admin'} siteId={site.id} />
}
