import { createFileRoute, redirect } from '@tanstack/react-router'

import { OrganizationLayout } from '@/components/OrganizationLayout'
import { AuthenticationRequired, meQueryOptions } from '@/lib/me'
import { queryClient } from '@/lib/queryClient'

export { fetchMe, meQueryOptions, useMe, type Me, type Membership, type Site } from '@/lib/me'

export function loginRedirect(pathname: string, search = ''): string {
  return `/login?next=${encodeURIComponent(`${pathname}${search}`)}`
}

export const Route = createFileRoute('/app/$org')({
  beforeLoad: async ({ location }) => {
    try {
      await queryClient.ensureQueryData(meQueryOptions)
    } catch (error) {
      if (error instanceof AuthenticationRequired) {
        throw redirect({
          to: '/login',
          search: { next: location.href },
        })
      }
      throw error
    }
  },
  component: OrganizationLayout,
})
