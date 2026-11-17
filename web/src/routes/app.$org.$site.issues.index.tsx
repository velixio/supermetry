import { createFileRoute } from '@tanstack/react-router'

import { IssuesTable } from '@/features/apps/IssuesTable'
import { appIssuesSearchInput, parseAppIssuesSearch } from '@/features/apps/search'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/issues/')({
  validateSearch: appIssuesSearchInput,
  component: IssuesRoute,
})

function IssuesRoute() {
  const { org, site: slug } = Route.useParams()
  const search = parseAppIssuesSearch(Route.useSearch())
  const navigate = Route.useNavigate()
  const { site, membership } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <IssuesTable
      org={org} site={slug} siteId={site.id} timezone={site.timezone ?? 'UTC'} search={search} canEdit={membership?.role !== 'viewer'}
      onSearch={(next) => void navigate({ search: appIssuesSearchInput({ ...next }) })}
    />
  )
}
