import { createFileRoute } from '@tanstack/react-router'

import { LinkStatsPage } from '@/features/links/LinkStatsPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/links/$id')({ component: LinkStatsRoute })

function LinkStatsRoute() {
  const { id, org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  const linkId = Number(id)
  if (!membership || !site || !Number.isSafeInteger(linkId) || linkId < 1) return null
  return <LinkStatsPage linkId={linkId} org={org} siteId={site.id} siteSlug={slug} timezone={site.timezone || 'UTC'} />
}
