import { createFileRoute } from '@tanstack/react-router'

import { EventDetail } from '@/features/apps/EventDetail'
import { useSite } from '@/features/settings/routeContext'

export const Route = createFileRoute('/app/$org/$site/issues_/$issue/events/$event')({ component: EventDetailRoute })

function EventDetailRoute() {
  const { org, site: slug, issue, event } = Route.useParams()
  const { site } = useSite(org, slug)
  const issueId = Number(issue)
  if (!site || site.kind !== 'app' || !Number.isInteger(issueId)) return <p className="text-sm text-muted-foreground">This page is only for apps.</p>
  return <EventDetail org={org} site={slug} siteId={site.id} issueId={issueId} eventId={event} />
}
