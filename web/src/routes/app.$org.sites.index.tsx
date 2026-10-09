import { useQueryClient } from '@tanstack/react-query'
import { createFileRoute } from '@tanstack/react-router'

import { deleteSite } from '@/features/sites/api'
import { SitesPage } from '@/features/sites/SitesPage'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/sites/')({ component: SitesRoute })

function SitesRoute() {
  const { org } = Route.useParams()
  const me = useMe()
  const queryClient = useQueryClient()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  if (!membership) return null

  return (
    <SitesPage
      org={org}
      role={membership.role}
      sites={membership.sites}
      onDelete={async (siteId) => {
        await deleteSite(siteId, membership.org.id)
        await queryClient.invalidateQueries({ queryKey: ['me'] })
      }}
    />
  )
}
