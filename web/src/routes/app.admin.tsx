import { createFileRoute, Link, Outlet, redirect } from '@tanstack/react-router'

import { AccountFrame } from '@/components/AccountFrame'
import { queryClient } from '@/lib/queryClient'
import { en } from '@/messages/en'
import { meQueryOptions, useMe } from './app.$org'

export const Route = createFileRoute('/app/admin')({
  beforeLoad: async ({ location }) => {
    const me = await queryClient.ensureQueryData(meQueryOptions)
    if (!me.user.instance_admin) throw redirect({ to: '/app' })
    if (location.pathname === '/app/admin' || location.pathname === '/app/admin/') throw redirect({ to: '/app/admin/users' })
  },
  component: AdminLayout,
})

const sections = [
  ['/app/admin/users', en.admin.users],
  ['/app/admin/organizations', en.admin.organizations],
  ['/app/admin/sites', en.admin.sites],
  ['/app/admin/settings', en.admin.settings],
  ['/app/admin/security', en.admin.security],
] as const

function AdminLayout() {
  const me = useMe()
  if (!me.data) return <main className="p-6" role="status">{en.admin.loading}</main>
  return (
    <AccountFrame me={me.data}>
      <nav aria-label={en.admin.nav} className="flex flex-wrap gap-1 border-b pb-3">
        {sections.map(([to, label]) => (
          <Link key={to} to={to} className="inline-flex min-h-11 items-center rounded-md px-3 text-sm hover:bg-muted" activeProps={{ className: 'bg-muted font-medium', 'aria-current': 'page' }}>{label}</Link>
        ))}
      </nav>
      <Outlet />
    </AccountFrame>
  )
}
