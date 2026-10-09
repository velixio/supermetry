import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { ProfileDetailPage } from '@/features/profiles/ProfileDetailPage'
import { useSite } from '@/features/settings/routeContext'
import { currentCursor, firstPage, nextPage, paginationSearch, previousPage } from '@/lib/search'

const profileSearch = z.object(paginationSearch)

export const Route = createFileRoute('/$org/$site/profiles/$id')({
  validateSearch: (search) => profileSearch.parse(search),
  component: ProfileRoute,
})

function ProfileRoute() {
  const search = Route.useSearch()
  const { id, org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const { membership, site } = useSite(org, siteSlug)
  if (!membership || !site) return null
  return (
    <ProfileDetailPage
      onDeleted={() => void navigate({ to: '/$org/$site/profiles', params: { org, site: siteSlug } })}
      cursor={currentCursor(search)}
      orgId={membership.org.id}
      org={org}
      page={search.page ?? 1}
      profileId={Number(id)}
      role={membership.role}
      siteId={site.id}
      site={siteSlug}
      onFirst={() => void navigate({ search: firstPage(search) })}
      onNext={(cursor) => void navigate({ search: nextPage(search, cursor) })}
      onPrevious={() => void navigate({ search: previousPage(search) })}
    />
  )
}
