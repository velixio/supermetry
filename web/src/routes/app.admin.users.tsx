import { createFileRoute } from '@tanstack/react-router'

import { parseAdminSearch } from '@/features/admin/AdminCommon'
import { AdminUsersPage } from '@/features/admin/AdminUsersPage'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/admin/users')({ validateSearch: parseAdminSearch, component: UsersRoute })

function UsersRoute() {
  const me = useMe()
  const { q, sort, order } = Route.useSearch()
  const navigate = Route.useNavigate()
  return (
    <AdminUsersPage
      currentUserId={me.data?.user.id}
      adminTotpEnabled={Boolean(me.data?.user.totp_enabled)}
      search={q ?? ''}
      onSearch={(value) => void navigate({ search: (prev) => ({ ...prev, q: value.trim() || undefined }) })}
      sort={{ sort, order }}
      onSort={(next) => void navigate({ search: (prev) => ({ ...prev, ...next }) })}
    />
  )
}
