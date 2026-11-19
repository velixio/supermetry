import { createFileRoute } from '@tanstack/react-router'

import { AdminUserDetailPage } from '@/features/admin/AdminUserDetailPage'

export const Route = createFileRoute('/app/admin/users_/$userId')({ component: UserRoute })

function UserRoute() {
  const { userId } = Route.useParams()
  return <AdminUserDetailPage userId={Number(userId)} />
}
