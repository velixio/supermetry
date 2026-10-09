import { createFileRoute, Link } from '@tanstack/react-router'

import { DatePicker } from '@/components/DatePicker'
import { FilterBar } from '@/components/FilterBar'
import { Button } from '@/components/ui/button'
import { RevenueReport } from '@/features/revenue/RevenueReport'
import { countryName } from '@/lib/country'
import { dashboardSearch, parseFilters, removeFilter, replaceFilters, resolveRange } from '@/lib/search'
import { en } from '@/messages/en'

import { useMe } from './app.$org'

export const Route = createFileRoute('/app/$org/$site/revenue')({
  validateSearch: (search) => dashboardSearch.parse(search),
  component: RevenueRoute,
})

function RevenueRoute() {
  const search = Route.useSearch()
  const { org, site: siteSlug } = Route.useParams()
  const navigate = Route.useNavigate()
  const me = useMe()
  const membership = me.data?.memberships.find((item) => item.org.slug === org)
  const site = membership?.sites.find((item) => item.slug === siteSlug || item.public_id === siteSlug || String(item.id) === siteSlug)
  if (!site) return null

  if (site.revenue_currency_state !== 'configured' || !site.revenue_currency) {
    const conflict = site.revenue_currency_state === 'conflict'
    return <section className="flex max-w-2xl flex-col items-start gap-4 rounded-xl border bg-card p-5" role="status">
      <h1 className="text-xl font-semibold tracking-tight">Revenue report unavailable</h1>
      <p className="text-sm text-muted-foreground">{conflict
        ? 'This project has mixed historical currencies. Revenue totals stay unavailable until the conflict is resolved without converting or merging amounts.'
        : 'Choose the project’s permanent revenue currency before viewing totals and breakdowns.'}</p>
      <Button asChild variant="outline"><Link to="/app/$org/$site/settings/general" params={{ org, site: siteSlug }}>Open site settings</Link></Button>
    </section>
  }

  const { filters, dropped } = parseFilters(search.f)
  const timezone = site.timezone || 'UTC'
  return <div className="flex min-w-0 flex-col gap-4">
    <div className="flex flex-wrap items-start justify-between gap-3">
      <FilterBar
        dropped={dropped}
        filterLabel={(filter) => `${filter.dimension} ${filter.operator} ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
        filters={filters}
        invalidNotice={en.dashboard.invalidFilter}
        removeLabel={(filter) => `Remove ${filter.dimension} filter ${filter.values.map((value) => filter.dimension === 'country' ? countryName(value) : value).join(', ')}`}
        onRemove={(index) => void navigate({ search: { ...search, ...removeFilter(search, index) } })}
        siteId={site.id}
        canEdit={membership?.role !== 'viewer'}
        onApply={(next) => void navigate({ search: { ...search, ...replaceFilters(search, next) } })}
      />
      <DatePicker labels={en.dashboard} search={search} onChange={(next) => void navigate({ search: next })} />
    </div>
    <RevenueReport
      siteId={site.id}
      currency={site.revenue_currency}
      timezone={timezone}
      dateRange={resolveRange(search.period, timezone, new Date(), search.from, search.to)}
      filters={filters}
      compare={search.compare}
      includeImported={search.include_imported}
    />
  </div>
}
