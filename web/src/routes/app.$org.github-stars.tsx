import { createFileRoute, Outlet, redirect } from '@tanstack/react-router'

import { queryClient } from '@/lib/queryClient'
import { meQueryOptions } from './app.$org'

// An organization-level page like Sites and Health, so the organization menu stays; the
// data itself is instance-wide, hence instance admins only. The guard covers every child.
export const Route = createFileRoute('/app/$org/github-stars')({
  beforeLoad: async ({ params }) => {
    const me = await queryClient.ensureQueryData(meQueryOptions)
    if (!me.user.instance_admin) throw redirect({ to: '/app/$org', params: { org: params.org } })
  },
  component: Outlet,
})
