import { createFileRoute } from '@tanstack/react-router'

import { ActiveUsersPage } from '@/features/apps/product/ActiveUsersPage'
import { appSearchInput, parseAppSearch } from '@/features/apps/search'
import { useSite } from '@/features/settings/routeContext'

import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/$site/users')({
  validateSearch: appSearchInput,
  component: UsersRoute,
})

function UsersRoute() {
  const { org, site: slug } = Route.useParams()
  const search = parseAppSearch(Route.useSearch())
  const navigate = Route.useNavigate()
  const { site } = useSite(org, slug)
  const me = useMe()
  const role = me.data?.memberships.find((item) => item.org.slug === org)?.role
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <ActiveUsersPage
      siteId={site.id} timezone={site.timezone ?? 'UTC'} search={search} canEdit={role !== undefined && role !== 'viewer'}
      onSearch={(next) => void navigate({ search: appSearchInput({ ...next }) })}
    />
  )
}
