import { createFileRoute } from '@tanstack/react-router'

import { GoalsPage } from '@/features/goals/GoalsPage'
import { resolveRange } from '@/lib/search'

import { useMe } from './$org'

export const Route = createFileRoute('/$org/$site/goals')({
  component: GoalsRoute,
})

function GoalsRoute() {
  const { org, site: siteSlug } = Route.useParams()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  if (!site) return null
  return <GoalsPage dateRange={resolveRange('30d', 'UTC', new Date())} siteId={site.id} />
}
