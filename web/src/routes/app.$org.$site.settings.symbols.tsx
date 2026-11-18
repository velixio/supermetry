import { createFileRoute } from '@tanstack/react-router'

import { DebugFiles } from '@/features/apps/DebugFiles'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/settings/symbols')({ component: SymbolsRoute })

function SymbolsRoute() {
  const { org, site: slug } = Route.useParams()
  const { membership, site } = useSite(org, slug)
  if (!membership || !site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return <DebugFiles siteId={site.id} role={membership.role} />
}
