import { createFileRoute } from '@tanstack/react-router'

import { CompatibilityMatrix } from '@/features/apps/CompatibilityMatrix'
import { appSearchInput, parseAppSearch } from '@/features/apps/search'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/compatibility')({
  validateSearch: appSearchInput,
  component: CompatibilityRoute,
})

function CompatibilityRoute() {
  const { org, site: slug } = Route.useParams()
  const search = parseAppSearch(Route.useSearch())
  const navigate = Route.useNavigate()
  const { site } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <CompatibilityMatrix
      org={org} site={slug} siteId={site.id} timezone={site.timezone ?? 'UTC'} search={search}
      onSearch={(next) => void navigate({ search: appSearchInput({ ...next }) })}
    />
  )
}
