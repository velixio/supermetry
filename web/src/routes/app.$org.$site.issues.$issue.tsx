import { createFileRoute } from '@tanstack/react-router'

import { IssueDetail } from '@/features/apps/IssueDetail'
import { appSearchInput, parseAppSearch } from '@/features/apps/search'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/issues/$issue')({
  validateSearch: appSearchInput,
  component: IssueDetailRoute,
})

function IssueDetailRoute() {
  const { org, site: slug, issue } = Route.useParams()
  const search = parseAppSearch(Route.useSearch())
  const { membership, site } = useSite(org, slug)
  const issueId = Number(issue)
  if (!site || site.kind !== 'app' || !Number.isInteger(issueId)) return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return <IssueDetail org={org} site={slug} siteId={site.id} issueId={issueId} timezone={site.timezone ?? 'UTC'} search={search} canEdit={membership?.role !== 'viewer'} />
}
