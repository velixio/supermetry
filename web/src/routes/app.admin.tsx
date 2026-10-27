import { createFileRoute, Link, Outlet, redirect } from '@tanstack/react-router'

import { ManagedNotice } from '@/features/admin/ManagedNotice'
import { createAuthConfigApi } from '@/features/auth/api'
import { PageContainer } from '@/components/PageContainer'
import { queryClient } from '@/lib/queryClient'
import { en } from '@/messages/en'
import { meQueryOptions } from './app.$org'

export const Route = createFileRoute('/app/admin')({
  beforeLoad: async ({ location }) => {
    const me = await queryClient.ensureQueryData(meQueryOptions)
    if (!me.user.instance_admin) throw redirect({ to: '/app' })
    // Decided before any admin page mounts, so a managed instance never fires an admin request.
    const config = await queryClient.ensureQueryData({ queryKey: ['auth-config'], queryFn: createAuthConfigApi(), staleTime: 300_000 }).catch(() => undefined)
    if (config?.managed === true) return { managed: true }
    if (location.pathname === '/app/admin' || location.pathname === '/app/admin/') throw redirect({ to: '/app/admin/users' })
    return { managed: false }
  },
  component: AdminLayout,
})

const sections = [
  ['/app/admin/users', en.admin.users],
  ['/app/admin/organizations', en.admin.organizations],
  ['/app/admin/sites', en.admin.sites],
  ['/app/admin/settings', en.admin.settings],
  ['/app/admin/security', en.admin.security],
  ['/app/admin/update', en.admin.update],
] as const

// Content inside the app shell: the administration section tabs and the page.
function AdminLayout() {
  const { managed } = Route.useRouteContext()
  if (managed) return <ManagedNotice />
  return (
    <PageContainer width="narrow">
      <nav aria-label={en.admin.nav} className="flex flex-wrap gap-1 border-b pb-3">
        {sections.map(([to, label]) => (
          <Link key={to} to={to} className="inline-flex min-h-11 items-center rounded-md px-3 text-sm hover:bg-muted" activeProps={{ className: 'bg-muted font-medium', 'aria-current': 'page' }}>{label}</Link>
        ))}
      </nav>
      <Outlet />
    </PageContainer>
  )
}
