import { useState } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { en } from '@/messages/en'
import { useAdminUsers, useUpdateAdminUser, type AdminUser, type AdminUserPatch } from './adminApi'
import { AdminPage, ErrorLine, SearchForm, cellClass, formatDate, tableClass } from './AdminCommon'

export function AdminUsersPage({ currentUserId }: { currentUserId?: number }) {
  const [search, setSearch] = useState('')
  const users = useAdminUsers(search)
  const update = useUpdateAdminUser()

  const act = (user: AdminUser, patch: AdminUserPatch, confirmation?: string) => {
    if (confirmation && !window.confirm(confirmation)) return
    update.mutate({ id: user.id, patch })
  }

  return (
    <AdminPage title={en.admin.users}>
      <SearchForm onSearch={setSearch} />
      <ErrorLine error={users.error ?? update.error} />
      {users.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {users.data && users.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      {users.data && users.data.data.length > 0 ? (
        <div className="overflow-x-auto rounded-lg border">
          <table className={tableClass}>
            <caption className="sr-only">{en.admin.users}</caption>
            <thead className="border-b bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr><th scope="col" className={cellClass}>User</th><th scope="col" className={cellClass}>Status</th><th scope="col" className={cellClass}>Last sign-in</th><th scope="col" className={cellClass}>Actions</th></tr>
            </thead>
            <tbody>
              {users.data.data.map((user) => (
                <tr key={user.id} className="border-b last:border-0" data-testid="admin-user-row">
                  <td className={cellClass}><div className="font-medium">{user.name}</div><div className="break-all text-muted-foreground">{user.email}</div></td>
                  <td className={cellClass}>
                    <div className="flex flex-wrap gap-1">
                      {user.instance_admin ? <Badge variant="secondary">{en.admin.instanceAdmin}</Badge> : null}
                      {user.disabled ? <Badge variant="destructive">{en.admin.disabled}</Badge> : null}
                      {user.password_reset_required ? <Badge variant="outline">{en.admin.resetRequired}</Badge> : null}
                      {user.totp_enabled ? <Badge variant="outline">{en.admin.twoFactorOn}</Badge> : null}
                    </div>
                  </td>
                  <td className={cellClass}>{user.last_login_at ? formatDate(user.last_login_at) : en.admin.neverSignedIn}</td>
                  <td className={cellClass}>
                    <div className="flex flex-wrap gap-1">
                      <Button size="sm" variant="outline" className="min-h-11" disabled={update.isPending || user.id === currentUserId}
                        aria-label={`${user.disabled ? en.admin.enable : en.admin.disable} ${user.email}`}
                        onClick={() => act(user, { disabled: !user.disabled }, user.disabled ? undefined : en.admin.confirmDisable)}>
                        {user.disabled ? en.admin.enable : en.admin.disable}
                      </Button>
                      <Button size="sm" variant="outline" className="min-h-11" disabled={update.isPending || user.password_reset_required}
                        aria-label={`${en.admin.requireReset}: ${user.email}`} onClick={() => act(user, { password_reset_required: true })}>
                        {en.admin.requireReset}
                      </Button>
                      <Button size="sm" variant="outline" className="min-h-11" disabled={update.isPending}
                        aria-label={`${user.instance_admin ? en.admin.demote : en.admin.promote}: ${user.email}`}
                        onClick={() => act(user, { instance_admin: !user.instance_admin }, user.instance_admin ? en.admin.confirmDemote : undefined)}>
                        {user.instance_admin ? en.admin.demote : en.admin.promote}
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </AdminPage>
  )
}
