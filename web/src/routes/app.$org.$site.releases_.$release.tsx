import { createFileRoute } from '@tanstack/react-router'

import { ReleaseDetail } from '@/features/apps/ReleaseDetail'
import { appSearchInput, parseAppSearch } from '@/features/apps/search'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/releases_/$release')({
  validateSearch: appSearchInput,
  component: ReleaseDetailRoute,
})

function ReleaseDetailRoute() {
  const { org, site: slug, release } = Route.useParams()
  const search = parseAppSearch(Route.useSearch())
  const navigate = Route.useNavigate()
  const { site } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <ReleaseDetail
      org={org} site={slug} siteId={site.id} timezone={site.timezone ?? 'UTC'} search={search} release={release}
      onSearch={(next) => void navigate({ search: appSearchInput({ ...next }) })}
    />
  )
}
