import { createFileRoute } from '@tanstack/react-router'

import { ImportsExportPage } from '@/features/settings/ImportsExportPage'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/$org/$site/settings/imports')({
  component: ImportsRoute,
})

function ImportsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  return membership && site ? (
    <ImportsExportPage orgId={membership.org.id} siteId={site.id} />
  ) : null
}
