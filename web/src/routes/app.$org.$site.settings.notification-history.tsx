import { createFileRoute } from '@tanstack/react-router'

import { NotificationHistoryPage } from '@/features/settings/NotificationHistoryPage'
import type { DeliveryStatus, HistoryFilters } from '@/features/settings/notificationHistoryApi'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/notification-history')({
  validateSearch: (search: Record<string, unknown>): HistoryFilters => ({
    status: ['pending', 'succeeded', 'failed'].includes(String(search.status)) ? String(search.status) as DeliveryStatus : undefined,
    channel: ['email', 'slack', 'discord', 'telegram', 'webhook'].includes(String(search.channel)) ? String(search.channel) : undefined,
    alertId: Number(search.alertId) > 0 ? Number(search.alertId) : undefined,
  }),
  component: NotificationHistoryRoute,
})

function NotificationHistoryRoute() {
  const { org, site: slug } = Route.useParams()
  const filters = Route.useSearch()
  const navigate = Route.useNavigate()
  const { membership, site } = useSite(org, slug)
  return membership && site ? <NotificationHistoryPage siteId={site.id} orgId={membership.org.id} filters={filters} onFilters={(search) => void navigate({ search, replace: true })} /> : null
}
