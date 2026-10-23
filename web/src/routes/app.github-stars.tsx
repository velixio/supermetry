import { createFileRoute, redirect } from '@tanstack/react-router'

import { queryClient } from '@/lib/queryClient'
import { meQueryOptions } from './app.$org'

// Entry point for pages outside an organization (account, admin): continue in the user's
// default organization, where the page sits in the normal menu.
export const Route = createFileRoute('/app/github-stars')({
  beforeLoad: async () => {
    const me = await queryClient.ensureQueryData(meQueryOptions)
    if (!me.user.instance_admin) throw redirect({ to: '/app' })
    const membership = me.memberships[0]
    if (!membership) throw redirect({ to: '/app/admin/users' })
    throw redirect({ to: '/app/$org/github-stars', params: { org: membership.org.slug }, replace: true })
  },
})
