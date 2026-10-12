import { createFileRoute } from '@tanstack/react-router'

import { LinksPage } from '@/features/links/LinksPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/links/')({ component: LinksRoute })

function LinksRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  if (!membership || !site) return null
  return <LinksPage canManage={membership.role !== 'viewer'} org={org} siteId={site.id} siteSlug={slug} />
}
