import { Outlet, createFileRoute, redirect } from '@tanstack/react-router'
import { AuthenticationRequired, meQueryOptions } from '@/lib/me'
import { queryClient } from '@/lib/queryClient'

export const Route = createFileRoute('/app')({
  beforeLoad: async ({ location }) => {
    try { await queryClient.ensureQueryData(meQueryOptions) }
    catch (error) {
      if (error instanceof AuthenticationRequired) throw redirect({ to: '/login', search: { next: location.href } })
      throw error
    }
  },
  component: AppEntry,
})

function AppEntry() {
  return <Outlet />
}
