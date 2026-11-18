import { createFileRoute } from '@tanstack/react-router'

import { ImportsExportPage } from '@/features/settings/ImportsExportPage'
import { useSite } from '@/features/settings/routeContext'

type ImportsSearch = { ga4?: 'connected' | 'denied' | 'error'; import?: number }

export const Route = createFileRoute('/app/$org/$site/settings/imports')({
  validateSearch: (search: Record<string, unknown>): ImportsSearch => {
    const ga4 = search.ga4 === 'connected' || search.ga4 === 'denied' || search.ga4 === 'error' ? search.ga4 : undefined
    const id = Number(search.import)
    return { ga4, import: Number.isInteger(id) && id > 0 ? id : undefined }
  },
  component: ImportsRoute,
})

function ImportsRoute() {
  const { org, site: slug } = Route.useParams()
  const search = Route.useSearch()
  const navigate = Route.useNavigate()
  const { membership, site } = useSite(org, slug)
  return membership && site ? (
    <ImportsExportPage
      orgId={membership.org.id}
      siteId={site.id}
      search={search}
      onSearchChange={(patch) => void navigate({ search: (previous) => ({ ...previous, ...patch }), replace: true })}
    />
  ) : null
}
