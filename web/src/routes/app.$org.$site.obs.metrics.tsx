import { createFileRoute } from '@tanstack/react-router'

import { MetricExplorer } from '@/features/obs/metrics/MetricExplorer'
import { useSite } from '@/features/settings/routeContext'
import { obsMetrics as t } from '@/messages/obsMetrics'

export const Route = createFileRoute('/app/$org/$site/obs/metrics')({
  component: MetricsRoute,
})

function MetricsRoute() {
  const { org, site: slug } = Route.useParams()
  const { site } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <div className="flex flex-col gap-4">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">{t.title}</h1>
        <p className="text-sm text-muted-foreground">{t.description}</p>
      </header>
      <MetricExplorer siteId={site.id} timezone={site.timezone ?? 'UTC'} />
    </div>
  )
}
