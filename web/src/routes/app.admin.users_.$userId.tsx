import { createFileRoute } from '@tanstack/react-router'

import { AdminUserDetailPage } from '@/features/admin/AdminUserDetailPage'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/admin/users_/$userId')({ component: UserRoute })

function UserRoute() {
  const { userId } = Route.useParams()
  const me = useMe()
  return <AdminUserDetailPage userId={Number(userId)} adminTotpEnabled={Boolean(me.data?.user.totp_enabled)} />
}
