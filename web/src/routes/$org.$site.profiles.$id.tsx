import { createFileRoute } from '@tanstack/react-router'

import { ProfileDetailPage } from '@/features/profiles/ProfileDetailPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/$site/profiles/$id')({ component: ProfileRoute })

function ProfileRoute() {
  const { id, org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const { membership, site } = useSite(org, siteSlug)
  if (!membership || !site) return null
  return (
    <ProfileDetailPage
      onDeleted={() => void navigate({ to: '/$org/$site/profiles', params: { org, site: siteSlug } })}
      orgId={membership.org.id}
      org={org}
      profileId={Number(id)}
      role={membership.role}
      siteId={site.id}
      site={siteSlug}
    />
  )
}
