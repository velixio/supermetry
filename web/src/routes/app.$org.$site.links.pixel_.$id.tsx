import { createFileRoute } from '@tanstack/react-router'

import { TrackingPixelPage } from '@/features/links/TrackingPixelPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/links/pixel_/$id')({ component: TrackingPixelRoute })

function TrackingPixelRoute() {
  const { id, org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  const pixelId = Number(id)
  if (!membership || !site || !Number.isSafeInteger(pixelId) || pixelId < 1) return null
  return (
    <TrackingPixelPage
      canManage={membership.role !== 'viewer'}
      org={org}
      pixelId={pixelId}
      siteId={site.id}
      siteSlug={slug}
      timezone={site.timezone || 'UTC'}
    />
  )
}
