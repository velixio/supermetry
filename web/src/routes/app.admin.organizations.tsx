import { createFileRoute } from '@tanstack/react-router'

import { parseAdminSearch } from '@/features/admin/AdminCommon'
import { AdminOrganizationsPage } from '@/features/admin/AdminOrganizationsPage'

export const Route = createFileRoute('/app/admin/organizations')({ validateSearch: parseAdminSearch, component: OrganizationsRoute })

function OrganizationsRoute() {
  const { q } = Route.useSearch()
  const navigate = Route.useNavigate()
  return <AdminOrganizationsPage search={q ?? ''} onSearch={(value) => void navigate({ search: value.trim() ? { q: value.trim() } : {} })} />
}
