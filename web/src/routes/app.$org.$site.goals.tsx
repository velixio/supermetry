import { createFileRoute } from '@tanstack/react-router'

import { GoalsPage } from '@/features/goals/GoalsPage'
import { resolveRange } from '@/lib/search'

import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/$site/goals')({
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
  return (
    <GoalsPage
      dateRange={resolveRange('30d', site.timezone || 'UTC', new Date())}
      siteId={site.id}
      timezone={site.timezone || 'UTC'}
      revenueCurrency={site.revenue_currency}
      revenueCurrencyState={site.revenue_currency_state}
    />
  )
}
