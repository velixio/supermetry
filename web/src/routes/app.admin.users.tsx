import { createFileRoute } from '@tanstack/react-router'

import { parseAdminSearch } from '@/features/admin/AdminCommon'
import { AdminUsersPage } from '@/features/admin/AdminUsersPage'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/admin/users')({ validateSearch: parseAdminSearch, component: UsersRoute })

function UsersRoute() {
  const me = useMe()
  const { q } = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <AdminUsersPage
      currentUserId={me.data?.user.id}
      adminTotpEnabled={Boolean(me.data?.user.totp_enabled)}
      search={q ?? ''}
      onSearch={(value) => void navigate({ search: value.trim() ? { q: value.trim() } : {} })}
    />
  )
}
