import { createFileRoute } from '@tanstack/react-router'

import { PageHeader } from '@/components/PageHeader'
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
      <PageHeader description={t.description} title={t.title} />
      <MetricExplorer siteId={site.id} timezone={site.timezone ?? 'UTC'} />
    </div>
  )
}
