import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'

import { HeatmapsPage } from '@/features/heatmaps/HeatmapsPage'
import type { HeatmapDevice } from '@/features/heatmaps/types'
import { dashboardSearch, periodValues, resolveRange, type Period } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './app.$org'

export const heatmapSearch = dashboardSearch.extend({
  path: z.string().catch(''),
  device: z.enum(['mobile', 'tablet', 'desktop']).catch('desktop'),
})

export const Route = createFileRoute('/app/$org/$site/heatmaps')({
  validateSearch: (search) => heatmapSearch.parse(search),
  component: HeatmapsRoute,
})

function HeatmapsRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) =>
    item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug,
  )
  if (!site || !membership) return null

  const timezone = site.timezone || 'UTC'
  const updatePeriod = (period: Period) => {
    void navigate({ search: { ...search, period, from: undefined, to: undefined } })
  }

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap justify-end gap-2">
        <select
          aria-label={en.dashboard.period}
          className="h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          value={search.period}
          onChange={(event) => updatePeriod(event.target.value as Period)}
        >
          {periodValues.map((period) => (
            <option key={period} value={period}>{en.dashboard.periods[period]}</option>
          ))}
        </select>
        {search.period === 'custom' ? (
          <>
            <input
              aria-label={en.dashboard.from}
              className="h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              type="date"
              value={search.from ?? ''}
              onChange={(event) => void navigate({ search: { ...search, from: event.target.value || undefined } })}
            />
            <input
              aria-label={en.dashboard.to}
              className="h-9 rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              type="date"
              value={search.to ?? ''}
              onChange={(event) => void navigate({ search: { ...search, to: event.target.value || undefined } })}
            />
          </>
        ) : null}
      </div>
      <HeatmapsPage
        siteId={site.id}
        orgId={membership.org.id}
        dateRange={resolveRange(search.period, timezone, new Date(), search.from, search.to)}
        device={search.device}
        path={search.path}
        onChange={(value: { device: HeatmapDevice; path: string }) => void navigate({ search: { ...search, ...value } })}
      />
    </div>
  )
}
