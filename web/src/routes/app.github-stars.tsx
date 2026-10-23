import { createFileRoute, redirect } from '@tanstack/react-router'

import { GitHubStarsPage } from '@/features/github-stars/GitHubStarsPage'
import { queryClient } from '@/lib/queryClient'
import { meQueryOptions } from './app.$org'

// Instance-wide data, so the page sits beside the account pages, not under an organization.
export const Route = createFileRoute('/app/github-stars')({
  beforeLoad: async () => {
    const me = await queryClient.ensureQueryData(meQueryOptions)
    if (!me.user.instance_admin) throw redirect({ to: '/app' })
  },
  component: GitHubStarsPage,
})
