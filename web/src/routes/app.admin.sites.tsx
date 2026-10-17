import { createFileRoute } from '@tanstack/react-router'

import { parseAdminSearch } from '@/features/admin/AdminCommon'
import { AdminSitesPage } from '@/features/admin/AdminSitesPage'

export const Route = createFileRoute('/app/admin/sites')({ validateSearch: parseAdminSearch, component: SitesRoute })

function SitesRoute() {
  const { q } = Route.useSearch()
  const navigate = Route.useNavigate()
  return <AdminSitesPage search={q ?? ''} onSearch={(value) => void navigate({ search: value.trim() ? { q: value.trim() } : {} })} />
}
