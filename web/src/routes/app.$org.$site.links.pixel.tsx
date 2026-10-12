import { createFileRoute } from '@tanstack/react-router'

import { PixelPage } from '@/features/links/PixelPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/links/pixel')({ component: PixelRoute })

function PixelRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  if (!membership || !site) return null
  return <PixelPage org={org} siteId={site.id} siteSlug={slug} timezone={site.timezone || 'UTC'} />
}
