import { lazy, Suspense } from 'react'
import { createFileRoute } from '@tanstack/react-router'

import { parseSitesOverviewSearch } from '@/features/sites/overviewSearch'
import { canCreateSites } from '@/components/Sidebar'
import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/sites/')({ validateSearch: parseSitesOverviewSearch, component: SitesRoute })

const SitesOverview = lazy(() => import('@/features/sites/SitesOverview').then((module) => ({ default: module.SitesOverview })))

function SitesRoute() {
  const { org } = Route.useParams()
  const me = useMe()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  if (!membership) return null

  return (
    <Suspense fallback={<div className="h-72 animate-pulse rounded-xl bg-muted" aria-label="Loading sites" role="status" />}><SitesOverview
      canAdd={canCreateSites(membership.role)}
      orgs={[{ ...membership.org, canCreate: canCreateSites(membership.role) }]}
      org={org}
      period={search.period ?? '24h'}
      query={search.q ?? ''}
      sort={search.sort ?? 'visitors'}
      view={me.data?.preferences?.sites_view ?? 'cards'}
      onSearchChange={(q) => void navigate({ search: { ...search, q } })}
      onOrgChange={() => undefined}
      onPeriodChange={(period) => void navigate({ search: { ...search, period } })}
      onSortChange={(sort) => void navigate({ search: { ...search, sort } })}
    /></Suspense>
  )
}
