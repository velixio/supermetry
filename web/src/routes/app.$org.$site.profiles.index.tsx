import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { ProfilesPage } from '@/features/profiles/ProfilesPage'
import { useSite } from '@/features/settings/routeContext'
import { currentCursor, firstPage, nextPage, paginationSearch, previousPage } from '@/lib/search'

const profilesSearch = z.object({
  search: z.string().optional().catch(undefined),
  ...paginationSearch,
})

export const Route = createFileRoute('/app/$org/$site/profiles/')({
  validateSearch: (search) => profilesSearch.parse(search),
  component: ProfilesRoute,
})

function ProfilesRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const { membership, site } = useSite(org, siteSlug)
  return membership && site ? (
    <ProfilesPage
      cursor={currentCursor(search)}
      orgId={membership.org.id}
      orgSlug={org}
      page={search.page ?? 1}
      search={search.search ?? ''}
      siteId={site.id}
      siteSlug={siteSlug}
      onFirst={() => void navigate({ search: firstPage(search) })}
      onNext={(cursor) => void navigate({ search: nextPage(search, cursor) })}
      onPrevious={() => void navigate({ search: previousPage(search) })}
      onSearchChange={(value) => void navigate({ search: firstPage({ ...search, search: value }) })}
    />
  ) : null
}
