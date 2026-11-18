import { useQuery } from '@tanstack/react-query'

import { Badge } from '@/components/ui/badge'
import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

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
  if (incidents.length === 0) return <p className="rounded-xl border bg-card p-5 text-sm text-muted-foreground">No incidents for this rule yet.</p>
  return (
    <ol aria-label="Incidents" className="flex flex-col gap-3">
      {incidents.map((incident) => (
        <li key={incident.id} className="grid gap-1 rounded-xl border bg-card p-4">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant={incident.resolved_at ? 'secondary' : 'destructive'}>{incident.resolved_at ? 'Resolved' : 'Open'}</Badge>
            <Badge variant="outline" className="capitalize">{incident.severity}</Badge>
            <span className="text-sm text-muted-foreground">{incident.group_key}</span>
          </div>
          <p className="text-sm text-muted-foreground">
            Opened {stamp(incident.opened_at)}{incident.resolved_at ? ` · Resolved ${stamp(incident.resolved_at)}` : ''} · Peak {incident.peak_value} · Threshold {incident.threshold}
          </p>
          <p className="text-sm text-muted-foreground">{incident.notified_at ? `Notified ${stamp(incident.notified_at)}` : 'No notice sent (silenced or no channel)'}</p>
        </li>
      ))}
    </ol>
  )
}
