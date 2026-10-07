import { createFileRoute } from '@tanstack/react-router'

import { dashboardSearch, parseFilters } from '../lib/search'
import { en } from '../messages/en'

export const Route = createFileRoute('/$org/$site/')({
  validateSearch: (search) => dashboardSearch.parse(search),
  component: DashboardStateRoute,
})

function DashboardStateRoute() {
  const search = Route.useSearch()
  const { filters } = parseFilters(search.f)

  return (
    <main
      className="min-h-dvh bg-background text-foreground"
      data-filter-count={filters.length}
    >
      <p className="sr-only">{en.loadingDashboard}</p>
    </main>
  )
}
