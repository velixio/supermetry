import { createFileRoute } from '@tanstack/react-router'

import { parseAdminSearch } from '@/features/admin/AdminCommon'
import { AdminSitesPage } from '@/features/admin/AdminSitesPage'

export const Route = createFileRoute('/app/admin/sites')({ validateSearch: parseAdminSearch, component: SitesRoute })

function SitesRoute() {
  const { q, sort, order } = Route.useSearch()
  const navigate = Route.useNavigate()
  return <AdminSitesPage search={q ?? ''} onSearch={(value) => void navigate({ search: (prev) => ({ ...prev, q: value.trim() || undefined }) })}
      sort={{ sort, order }}
      onSort={(next) => void navigate({ search: (prev) => ({ ...prev, ...next }) })} />
}
