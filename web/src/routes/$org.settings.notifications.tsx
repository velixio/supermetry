import { createFileRoute } from '@tanstack/react-router'

import { NotificationsPage } from '@/features/settings/NotificationsPage'
import { useOrganization } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/settings/notifications')({ component: NotificationsRoute })

function NotificationsRoute() {
  const { org } = Route.useParams()
  const membership = useOrganization(org)
  return membership ? (
    <NotificationsPage
      orgId={membership.org.id}
      canManage={membership.role === 'owner' || membership.role === 'admin'}
    />
  ) : null
}
