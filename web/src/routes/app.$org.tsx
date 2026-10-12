import { createFileRoute } from '@tanstack/react-router'

import { OrganizationLayout } from '@/components/OrganizationLayout'

export { fetchMe, meQueryOptions, useMe, type Me, type Membership, type Site } from '@/lib/me'

export function loginRedirect(pathname: string, search = ''): string {
  return `/login?next=${encodeURIComponent(`${pathname}${search}`)}`
}

export const Route = createFileRoute('/app/$org')({
  component: OrganizationLayout,
})
