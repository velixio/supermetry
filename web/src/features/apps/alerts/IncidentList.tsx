import { useQuery } from '@tanstack/react-query'

import { Badge } from '@/components/ui/badge'
import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'
import { uiAlerts as t } from '@/messages/uiAlerts'

export type AlertIncident = components['schemas']['AlertIncident']

export function useIncidents(siteId: number, orgId: number, alertId: number | undefined) {
  return useQuery({
    queryKey: ['sites', siteId, 'alerts', alertId, 'incidents'],
    enabled: alertId !== undefined,
    queryFn: () => privateRequest<{ data: AlertIncident[] }>('GET', `/api/private/sites/${siteId}/alerts/${alertId}/incidents?limit=20`, { orgId }),
  })
}

const stamp = (value: string) => new Date(value).toLocaleString()

/** One row per incident: when it opened and resolved, its peak, and whether anyone was told. */
export function IncidentList({ incidents }: { incidents: AlertIncident[] }) {
  if (incidents.length === 0) return <p className="rounded-lg bg-card p-5 text-sm text-muted-foreground">{t.incidents.empty}</p>
  return (
    <ol aria-label={t.incidents.list} className="flex flex-col gap-3">
      {incidents.map((incident) => (
        <li key={incident.id} className="grid gap-1 rounded-lg bg-card p-4">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={incident.resolved_at ? 'secondary' : 'destructive'}>{incident.resolved_at ? t.incidents.resolved : t.incidents.open}</Badge>
            <Badge variant="outline" className="capitalize">{incident.severity}</Badge>
            <span className="text-sm text-muted-foreground">{incident.group_key}</span>
          </div>
          <p className="text-sm text-muted-foreground">
            {incident.resolved_at
              ? t.incidents.summaryResolved(stamp(incident.opened_at), stamp(incident.resolved_at), incident.peak_value, incident.threshold)
              : t.incidents.summary(stamp(incident.opened_at), incident.peak_value, incident.threshold)}
          </p>
          <p className="text-sm text-muted-foreground">{incident.notified_at ? t.incidents.notified(stamp(incident.notified_at)) : t.incidents.notNotified}</p>
        </li>
      ))}
    </ol>
  )
}
