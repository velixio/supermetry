import { ReferenceLine } from 'recharts'

import type { AppDeployRow } from './api'
import { dayInZone } from './format'

export type DeployMarker = { key: number; day: string; label: string; title: string }

/** One marker per deploy whose local day is one of the chart's days; deploys outside the plotted range are dropped. */
export function deployMarkers(deploys: readonly AppDeployRow[], timezone: string, days: readonly string[]): DeployMarker[] {
  const plotted = new Set(days)
  return deploys.flatMap((deploy) => {
    const day = dayInZone(deploy.at, timezone)
    if (!plotted.has(day)) return []
    const detail = [deploy.environment, deploy.name].filter(Boolean).join(' · ')
    return [{ key: deploy.id, day, label: deploy.release, title: detail ? `${deploy.release} (${detail})` : deploy.release }]
  })
}

/** Recharts children: spread the result into a chart whose x axis is the day string. */
export function deployReferenceLines(markers: readonly DeployMarker[], yAxisId?: string) {
  return markers.map((marker) => (
    <ReferenceLine key={marker.key} x={marker.day} yAxisId={yAxisId} stroke="var(--chart-5)" strokeDasharray="4 3" ifOverflow="visible" label={{ value: marker.label, position: 'insideTopRight', fontSize: 10 }} />
  ))
}

/** Text list of the same markers, for readers who cannot hover the chart. */
export function DeployList({ deploys, timezone }: { deploys: readonly AppDeployRow[]; timezone: string }) {
  if (deploys.length === 0) return null
  return (
    <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-label="Deploys">
      {deploys.map((deploy) => (
        <li key={deploy.id}>
          <span aria-hidden className="mr-1 inline-block h-3 border-l border-dashed align-middle" style={{ borderColor: 'var(--chart-5)' }} />
          <span className="font-medium text-foreground">{deploy.release}</span> {dayInZone(deploy.at, timezone)}
          {deploy.environment ? ` · ${deploy.environment}` : ''}
          {deploy.name ? ` · ${deploy.name}` : ''}
        </li>
      ))}
    </ul>
  )
}
