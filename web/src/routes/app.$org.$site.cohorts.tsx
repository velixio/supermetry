import { createFileRoute } from '@tanstack/react-router'

import { DatePicker } from '@/components/DatePicker'
import { CohortsPage } from '@/features/cohorts/CohortsPage'
import { isAnonymousTracking } from '@/lib/tracking-policy'
import { dashboardSearch, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/$site/cohorts')({
  validateSearch: (search) => dashboardSearch.parse(search),
  component: CohortsRoute,
})

function CohortsRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  if (!site) return null

  const policy = isAnonymousTracking(site) ? 'anonymous' : site.profiles_enabled ? undefined : 'profiles_disabled'
  const timezone = site.timezone || 'UTC'
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {policy ? null : (
        <div className="flex flex-wrap items-start justify-end gap-3">
          <DatePicker labels={en.dashboard} search={search} onChange={(next) => void navigate({ search: next })} />
        </div>
      )}
      <CohortsPage
        canEdit={membership?.role !== 'viewer'}
        dateRange={resolveRange(search.period, timezone, new Date(), search.from, search.to)}
        orgSlug={org}
        policy={policy}
        siteId={site.id}
        siteSlug={siteSlug}
      />
    </div>
  )
}
