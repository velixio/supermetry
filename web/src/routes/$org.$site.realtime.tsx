import { createFileRoute } from '@tanstack/react-router'

import { RealtimePage } from '@/features/realtime/RealtimePage'
import { resolveRange } from '@/lib/search'

import { useMe } from './$org'

export const Route = createFileRoute('/$org/$site/realtime')({
  component: RealtimeRoute,
})

function RealtimeRoute() {
  const { org, site: siteSlug } = Route.useParams()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  if (!site) return null
  const timezone = site.timezone || 'UTC'
  return <RealtimePage dateRange={resolveRange('today', timezone, new Date())} siteId={site.id} timezone={timezone} />
}
