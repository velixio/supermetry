import { createFileRoute, Link, Outlet, redirect, useRouter } from '@tanstack/react-router'
import { PageContainer } from '@/components/PageContainer'
import { DeletedOrgsList } from '@/features/lifecycle/DeletedOrgsList'
import { queryClient } from '@/lib/queryClient'
import { meQueryOptions } from './app.$org'

export const Route = createFileRoute('/app/account')({
  beforeLoad: async ({ location }) => {
    if (location.pathname === '/app/account' || location.pathname === '/app/account/') {
      throw redirect({ to: '/app/account/profile' })
    }
    await queryClient.ensureQueryData(meQueryOptions)
  },
  component: AccountLayout,
})

// Content inside the app shell: the account section tabs, the page, and restorable organizations.
function AccountLayout() {
  const router = useRouter()
  return (
    <PageContainer width="narrow">
      <AccountSettingsNavigation />
      <Outlet />
      <DeletedOrgsList navigate={(path) => router.history.push(path)} />
    </PageContainer>
  )
}

export function AccountSettingsNavigation() {
  return (
    <nav aria-label="Account settings" className="flex flex-wrap gap-1 border-b pb-3">
      {([
        ['/app/account/profile', 'Profile'], ['/app/account/security', 'Security'], ['/app/account/sessions', 'Sessions'],
      ] as const).map(([to, label]) => (
        <Link key={to} to={to} className="inline-flex min-h-11 items-center rounded-md px-3 text-sm hover:bg-muted" activeProps={{ className: 'bg-muted font-medium', 'aria-current': 'page' }}>{label}</Link>
      ))}
    </nav>
  )
}
