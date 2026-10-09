import { createFileRoute, Link, Outlet, redirect } from '@tanstack/react-router'
import { AccountFrame } from '@/components/AccountFrame'
import { queryClient } from '@/lib/queryClient'
import { meQueryOptions, useMe } from './$org'

export const Route = createFileRoute('/account')({
  beforeLoad: async ({ location }) => {
    if (location.pathname === '/account' || location.pathname === '/account/') {
      throw redirect({ to: '/account/profile' })
    }
    await queryClient.ensureQueryData(meQueryOptions)
  },
  component: AccountLayout,
})

function AccountLayout() {
  const me = useMe()
  if (!me.data) return <main className="p-6" role="status">Loading account…</main>
  return (
    <AccountFrame me={me.data}>
      <nav aria-label="Account settings" className="flex flex-wrap gap-1 border-b pb-3">
        {([
          ['/account/profile', 'Profile'], ['/account/security', 'Security'], ['/account/sessions', 'Sessions'],
        ] as const).map(([to, label]) => (
          <Link key={to} to={to} className="rounded-md px-3 py-2 text-sm hover:bg-accent" activeProps={{ className: 'bg-accent font-medium', 'aria-current': 'page' }}>{label}</Link>
        ))}
      </nav>
      <Outlet />
    </AccountFrame>
  )
}
