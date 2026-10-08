import { createFileRoute } from '@tanstack/react-router'

import { ProfilesPage } from '@/features/profiles/ProfilesPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/$site/profiles/')({ component: ProfilesRoute })

function ProfilesRoute() {
  const { org, site: siteSlug } = Route.useParams()
  const { membership, site } = useSite(org, siteSlug)
  return membership && site ? (
    <ProfilesPage orgId={membership.org.id} orgSlug={org} siteId={site.id} siteSlug={siteSlug} />
  ) : null
}
