import { createFileRoute } from '@tanstack/react-router'

import { TraceView } from '@/features/apps/performance/TraceWaterfall'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/traces/$trace')({
  component: TraceRoute,
})

function TraceRoute() {
  const { org, site: slug, trace } = Route.useParams()
  const { site } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return <TraceView org={org} site={slug} siteId={site.id} traceId={trace} timezone={site.timezone ?? 'UTC'} />
}
