import { createFileRoute } from '@tanstack/react-router'

import { AdminUsersPage } from '@/features/admin/AdminUsersPage'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/admin/users')({ component: UsersRoute })

function UsersRoute() {
  const me = useMe()
  return <AdminUsersPage currentUserId={me.data?.user.id} />
}
