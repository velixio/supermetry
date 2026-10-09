import { createFileRoute } from '@tanstack/react-router'

import { FunnelsPage } from '@/features/funnels/FunnelsPage'
import { resolveRange } from '@/lib/search'

import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/$site/funnels/')({
  component: FunnelsRoute,
})

function FunnelsRoute() {
  const { org, site: siteSlug } = Route.useParams()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  if (!site) return null
  return <FunnelsPage dateRange={resolveRange('30d', 'UTC', new Date())} siteId={site.id} />
}
