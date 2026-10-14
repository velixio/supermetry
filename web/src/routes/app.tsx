import { createFileRoute, redirect, useRouterState } from '@tanstack/react-router'

import { AppShell } from '@/components/AppShell'
import { AuthenticationRequired, meQueryOptions, useMe } from '@/lib/me'
import { queryClient } from '@/lib/queryClient'
import { en } from '@/messages/en'

export const Route = createFileRoute('/app')({
  beforeLoad: async ({ location }) => {
    try { await queryClient.ensureQueryData(meQueryOptions) }
    catch (error) {
      if (error instanceof AuthenticationRequired) throw redirect({ to: '/login', search: { next: location.href } })
      throw error
    }
  },
  component: AppLayout,
})

/**
 * Every /app route renders inside this one shell, so moving between account, admin and
 * organization pages never remounts it. The organization comes from the URL; the sites
 * overview, account and admin pages have none.
 */
function AppLayout() {
  const me = useMe()
  const org = useRouterState({
    select: (state) => {
      const params = state.matches.at(-1)?.params
      return params && 'org' in params ? params.org : undefined
    },
  })
  if (!me.data) {
    // Only before the shell exists; beforeLoad has normally filled the cache already.
    return (
      <main className="grid min-h-dvh place-items-center px-6">
        <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>
      </main>
    )
  }
  const membership = org === undefined ? undefined : me.data.memberships.find((item) => item.org.slug === org)
  return <AppShell me={me.data} membership={membership} />
}
