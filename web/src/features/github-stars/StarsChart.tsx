import { Bar, CartesianGrid, ComposedChart, Line, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { fmt } from '@/lib/format'
import { activeLocale } from '@/lib/i18n/intl'
import { en } from '@/messages/en'
import type { StarsAnnotation, StarsGranularity, StarsPoint } from './githubStarsApi'

const stepMs: Record<StarsGranularity, number> = { hour: 3_600_000, day: 86_400_000 }

/** The bucket (its `bucket` string) an annotation falls in, or undefined when it is outside the shown range. */
export function markerBucket(points: StarsPoint[], granularity: StarsGranularity, at: string): string | undefined {
  const time = Date.parse(at)
  if (Number.isNaN(time) || points.length === 0) return undefined
  for (let i = points.length - 1; i >= 0; i--) {
    const start = Date.parse(points[i].bucket)
    if (time >= start) return time < start + stepMs[granularity] ? points[i].bucket : undefined
  }
  return undefined
}

function bucketLabel(bucket: string, granularity: StarsGranularity, timeZone?: string) {
  // The bucket carries its own offset; format the wall clock of the configured zone, not the browser's.
  const options: Intl.DateTimeFormatOptions = granularity === 'hour'
    ? { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZone }
    : { month: 'short', day: 'numeric', timeZone }
  return new Date(bucket).toLocaleString(activeLocale(), options)
}

type Props = {
  points: StarsPoint[]
  granularity: StarsGranularity
  timeZone?: string
  annotations: StarsAnnotation[]
}

/** Gained and lost bars with the cumulative total line. The table below is the accessible form of the chart. */
export function StarsChart({ points, granularity, timeZone, annotations }: Props) {
  const rows = points.map((point) => ({ ...point, lostBelow: -point.lost }))
  const markers = annotations.flatMap((annotation) => {
    const bucket = markerBucket(points, granularity, annotation.at)
    return bucket ? [{ id: annotation.id, label: annotation.label, bucket }] : []
  })
  const quiet = points.every((point) => point.gained === 0 && point.lost === 0)
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {quiet ? <p className="text-sm text-muted-foreground">{en.githubStars.noData}</p> : null}
      <div role="img" aria-label={en.githubStars.chartLabel} className="h-64 w-full min-w-0">
        <ResponsiveContainer width="100%" height="100%">
          <ComposedChart data={rows} margin={{ top: 16, right: 8, bottom: 0, left: 0 }} stackOffset="sign">
            <CartesianGrid vertical={false} strokeDasharray="3 3" />
            <XAxis dataKey="bucket" tickFormatter={(value: string) => bucketLabel(value, granularity, timeZone)} minTickGap={32} tickMargin={6} fontSize={12} />
            <YAxis yAxisId="change" allowDecimals={false} width={36} fontSize={12} />
            <YAxis yAxisId="total" orientation="right" allowDecimals={false} width={44} fontSize={12} domain={['auto', 'auto']} />
            <Tooltip labelFormatter={(value) => bucketLabel(String(value), granularity, timeZone)} formatter={(value, name) => [fmt.full(Math.abs(Number(value))), name]} />
            <Bar yAxisId="change" dataKey="gained" name={en.githubStars.gained} stackId="change" fill="var(--chart-3)" />
            <Bar yAxisId="change" dataKey="lostBelow" name={en.githubStars.lost} stackId="change" fill="var(--destructive)" />
            <Line yAxisId="total" dataKey="cumulative" name={en.githubStars.cumulative} type="monotone" stroke="var(--chart-1)" strokeWidth={2} dot={false} />
            {markers.map((marker) => (
              <ReferenceLine key={marker.id} yAxisId="change" x={marker.bucket} stroke="var(--muted-foreground)" strokeDasharray="4 3" ifOverflow="extendDomain" />
            ))}
          </ComposedChart>
        </ResponsiveContainer>
      </div>
      <ul className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground" aria-hidden="true">
        <li className="flex items-center gap-1"><span className="size-2.5 rounded-sm" style={{ background: 'var(--chart-3)' }} />{en.githubStars.gained}</li>
        <li className="flex items-center gap-1"><span className="size-2.5 rounded-sm" style={{ background: 'var(--destructive)' }} />{en.githubStars.lost}</li>
        <li className="flex items-center gap-1"><span className="size-2.5 rounded-sm" style={{ background: 'var(--chart-1)' }} />{en.githubStars.cumulative}</li>
      </ul>
      <table className="sr-only">
        <caption>{en.githubStars.chartTable}</caption>
        <thead><tr><th scope="col">{en.githubStars.bucket}</th><th scope="col">{en.githubStars.gained}</th><th scope="col">{en.githubStars.lost}</th><th scope="col">{en.githubStars.cumulative}</th></tr></thead>
        <tbody>
          {points.map((point) => {
            const marked = markers.filter((marker) => marker.bucket === point.bucket)
            return (
              <tr key={point.bucket}>
                <th scope="row">{bucketLabel(point.bucket, granularity, timeZone)}{marked.map((marker) => ` · ${en.githubStars.annotationMarker(marker.label)}`)}</th>
                <td>{point.gained}</td><td>{point.lost}</td><td>{point.cumulative}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
