import { createFileRoute } from '@tanstack/react-router'

import { FunnelsPage } from '@/features/funnels/FunnelsPage'
import { resolveRange } from '@/lib/search'

import { useMe } from './$org'

export const Route = createFileRoute('/$org/$site/funnels/$id')({
  component: FunnelRoute,
})

function FunnelRoute() {
  const { id, org, site: siteSlug } = Route.useParams()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  const funnelId = Number(id)
  if (!site || !Number.isSafeInteger(funnelId) || funnelId < 1) return null
  return <FunnelsPage dateRange={resolveRange('30d', 'UTC', new Date())} funnelId={funnelId} siteId={site.id} />
}
