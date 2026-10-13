import { useEffect } from 'react'
import { getRouteApi, useRouter } from '@tanstack/react-router'

import { canCreateSites } from '@/components/Sidebar'
import { en } from '@/messages/en'
import type { Me } from '@/routes/app.$org'

import { SitesOverview } from './SitesOverview'

const route = getRouteApi('/app/')

export function SitesOverviewLanding({ me }: { me: Me }) {
  const router = useRouter()
  const search = route.useSearch()
  const navigate = route.useNavigate()
  const target = landingTarget(me)

  useEffect(() => {
    if (target) router.history.replace(target)
  }, [router, target])

  if (target) return <p className="text-sm text-muted-foreground" role="status">{en.loadingDashboard}</p>

  return (
    <SitesOverview
      canAdd={me.memberships.some((item) => canCreateSites(item.role))}
      orgs={me.memberships.map((item) => ({ ...item.org, canCreate: canCreateSites(item.role) }))}
      org={search.org ?? ''}
      period={search.period ?? '24h'}
      query={search.q ?? ''}
      sort={search.sort ?? 'visitors'}
      view={me.preferences?.sites_view ?? 'cards'}
      onSearchChange={(q) => void navigate({ search: { ...search, q } })}
      onOrgChange={(org) => void navigate({ search: { ...search, org } })}
      onPeriodChange={(period) => void navigate({ search: { ...search, period } })}
      onSortChange={(sort) => void navigate({ search: { ...search, sort } })}
    />
  )
}

export function landingTarget(me: Me): string | undefined {
  const sites = me.memberships.flatMap((membership) => membership.sites.map((site) => ({ membership, site })))
  if (sites.length === 0 || (me.preferences?.landing_page ?? 'last_site') === 'overview') return undefined
  const selected = sites.find(({ site }) => site.id === me.preferences?.last_site_id) ?? sites[0]
  return selected ? `/app/${selected.membership.org.slug}/${selected.site.slug ?? selected.site.public_id ?? String(selected.site.id)}` : undefined
}
