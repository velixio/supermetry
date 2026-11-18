import { createFileRoute, useNavigate } from '@tanstack/react-router'

import { PageHeader } from '@/components/PageHeader'
import { otlpEndpoint } from '@/features/apps/sdkSnippets'
import { LogSearch } from '@/features/obs/logs/LogSearch'
import { useSite } from '@/features/settings/routeContext'
import { obsLogs as t } from '@/messages/obsLogs'

export const Route = createFileRoute('/app/$org/$site/obs/logs')({
  validateSearch: (search: Record<string, unknown>): { q?: string } => (typeof search.q === 'string' && search.q ? { q: search.q.slice(0, 300) } : {}),
  component: LogsRoute,
})

function LogsRoute() {
  const { org, site: slug } = Route.useParams()
  const { q } = Route.useSearch()
  const navigate = useNavigate({ from: Route.fullPath })
  const { site } = useSite(org, slug)
  if (!site || site.kind !== 'app') return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return (
    <div className="flex flex-col gap-4">
      <PageHeader description={t.description} title={t.title} />
      <p className="text-sm text-muted-foreground">OTLP URL: <code className="font-mono">{otlpEndpoint(window.location.origin, site.id)}</code></p>
      <LogSearch key={q ?? ''} org={org} site={slug} siteId={site.id} timezone={site.timezone ?? 'UTC'} initialQuery={q ?? ''}
        onQueryChange={(next) => void navigate({ search: next ? { q: next } : {}, replace: true })} />
    </div>
  )
}
