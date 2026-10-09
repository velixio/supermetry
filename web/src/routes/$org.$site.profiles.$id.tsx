import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { ProfileDetailPage } from '@/features/profiles/ProfileDetailPage'
import { useSite } from '@/features/settings/routeContext'

const profileSearch = z.object({
  replay: z.string().regex(/^[1-9][0-9]*$/).optional().catch(undefined),
})

export const Route = createFileRoute('/$org/$site/profiles/$id')({
  validateSearch: (search) => profileSearch.parse(search),
  component: ProfileRoute,
})

function ProfileRoute() {
  const { id, org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const search = Route.useSearch()
  const { membership, site } = useSite(org, siteSlug)
  if (!membership || !site) return null
  return (
    <ProfileDetailPage
      onDeleted={() => void navigate({ to: '/$org/$site/profiles', params: { org, site: siteSlug } })}
      orgId={membership.org.id}
      org={org}
      profileId={Number(id)}
      replayId={search.replay}
      role={membership.role}
      siteId={site.id}
      site={siteSlug}
      onReplayChange={(replay, options) => void navigate({ search: { ...search, replay }, replace: options?.replace })}
    />
  )
}
