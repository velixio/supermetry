import { createFileRoute, useNavigate } from '@tanstack/react-router'

import { IntegrationsPage, type IntegrationResult } from '@/features/integrations/IntegrationsPage'
import { useSite } from '@/features/settings/routeContext'

type IntegrationsSearch = { integration?: string; status?: IntegrationResult }

export const Route = createFileRoute('/app/$org/$site/settings/integrations')({
  validateSearch: (search: Record<string, unknown>): IntegrationsSearch => ({
    integration: typeof search.integration === 'string' ? search.integration : undefined,
    status: search.status === 'connected' || search.status === 'denied' || search.status === 'error' ? search.status : undefined,
  }),
  component: IntegrationsRoute,
})

function IntegrationsRoute() {
  const { org, site: slug } = Route.useParams()
  const search = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const { membership, site } = useSite(org, slug)
  return membership && site ? (
    <IntegrationsPage
      onDismissResult={() => void navigate({ search: {}, replace: true })}
      orgId={membership.org.id}
      result={search.status}
      role={membership.role}
      siteId={site.id}
    />
  ) : null
}
