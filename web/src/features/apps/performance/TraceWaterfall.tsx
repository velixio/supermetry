import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TraceLogs } from '@/features/obs/logs/TraceLogs'
import { obsLogs as logsText } from '@/messages/obsLogs'

import { formatMs } from '../format'
import { useTrace, type TraceData } from './api'

type TraceSpan = TraceData['spans'][number]

/** A span failed when its status is not ok or cancelled, or it answered with a 5xx. */
export function spanFailed(span: TraceSpan): boolean {
  return !['', 'ok', 'cancelled'].includes(span.status) || (span.http_status ?? 0) >= 500
}

/** Offset and width of a bar as percentages of the whole trace; every bar stays visible and inside the track. */
export function barGeometry(span: TraceSpan, totalMs: number): { left: number; width: number } {
  if (totalMs <= 0) return { left: 0, width: 100 }
  const left = Math.min(Math.max((span.start_offset_ms / totalMs) * 100, 0), 100)
  const width = Math.min(Math.max((span.duration_ms / totalMs) * 100, 0.5), 100 - left)
  return { left, width }
}

/** The trace as a waterfall of plain divs: indentation shows depth, the bar shows start and duration. */
export function TraceWaterfall({ org, site, trace }: { org: string; site: string; trace: TraceData }) {
  const total = Math.max(trace.duration_ms, ...trace.spans.map((span) => span.start_offset_ms + span.duration_ms), 0)
  const errorsBySpan = new Map<string, number>()
  for (const error of trace.errors) if (error.span_id) errorsBySpan.set(error.span_id, (errorsBySpan.get(error.span_id) ?? 0) + 1)
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {trace.truncated ? <p role="status" className="rounded-md bg-muted/50 px-3 py-2 text-sm">Showing the first 10,000 spans.</p> : null}
      <ol className="flex min-w-0 flex-col divide-y rounded-lg border" aria-label="Spans">
        {trace.spans.map((span) => {
          const failed = spanFailed(span) || errorsBySpan.has(span.span_id)
          const { left, width } = barGeometry(span, total)
          const label = span.name || span.description || span.op || span.span_id
          return (
            <li key={span.span_id} data-failed={failed ? 'true' : 'false'} className="grid min-w-0 grid-cols-[minmax(10rem,2fr)_3fr] items-center gap-3 px-3 py-1.5 text-sm">
              <div className="min-w-0" style={{ paddingLeft: `${Math.min(span.depth, 12) * 12}px` }}>
                <div className="flex min-w-0 items-center gap-2">
                  {span.op ? <span className="shrink-0 text-xs text-muted-foreground">{span.op}</span> : null}
                  <span className="truncate font-medium" title={span.description || label}>{label}</span>
                  {failed ? <Badge variant="destructive">error</Badge> : null}
                </div>
                {span.description && span.description !== label ? <p className="truncate font-mono text-xs text-muted-foreground" title={span.description}>{span.description}</p> : null}
              </div>
              <div className="relative h-5 rounded bg-muted" role="img" aria-label={`${label}: starts at ${formatMs(span.start_offset_ms)}, lasts ${formatMs(span.duration_ms)}`}>
                <div
                  data-testid="span-bar"
                  data-left={left.toFixed(1)}
                  className={`absolute top-0 h-full rounded ${failed ? 'bg-destructive' : span.is_segment ? 'bg-chart-1' : 'bg-chart-2'}`}
                  style={{ left: `${left}%`, width: `${width}%` }}
                />
                <span className="absolute inset-y-0 right-1 flex items-center text-xs tabular-nums text-foreground">{formatMs(span.duration_ms)}</span>
              </div>
            </li>
          )
        })}
      </ol>
      {trace.errors.length > 0 ? (
        <section aria-label="Errors in this trace" className="flex flex-col gap-2">
          <h2 className="text-base font-semibold">Errors in this trace</h2>
          <ul className="flex flex-col gap-1 text-sm">
            {trace.errors.map((error) => (
              <li key={error.event_id} className="flex flex-wrap items-baseline gap-2">
                {error.issue_id === null
                  ? <span className="font-medium">{error.title}</span>
                  : <Link className="font-medium underline" to="/app/$org/$site/issues/$issue" params={{ org, site, issue: String(error.issue_id) }}>{error.title}</Link>}
                <time className="text-xs text-muted-foreground">{error.ts.slice(0, 19).replace('T', ' ')}</time>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
    </div>
  )
}

/** The trace page: loads one trace and shows its waterfall. */
export function TraceView({ org, site, siteId, traceId, timezone = 'UTC' }: { org: string; site: string; siteId: number; traceId: string; timezone?: string }) {
  const trace = useTrace(siteId, traceId)
  const back = <Link className="text-sm font-medium underline" to="/app/$org/$site/performance" params={{ org, site }}>Back to performance</Link>
  return (
    <PageContainer>
      <PageHeader title="Trace" description={trace.data ? <span><span className="font-mono">{trace.data.trace_id}</span> · {formatMs(trace.data.duration_ms)} · {trace.data.spans.length} spans</span> : <span className="font-mono">{traceId}</span>} />
      {back}
      {trace.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading trace…</p> : null}
      {trace.isError ? <p role="alert" className="text-sm text-destructive">Trace not found. Raw spans are kept for 14 days at most: {trace.error.message}</p> : null}
      {trace.data ? (
        <Tabs defaultValue="spans">
          <TabsList>
            <TabsTrigger value="spans">{logsText.traceSpans}</TabsTrigger>
            <TabsTrigger value="logs">{logsText.traceLogs}</TabsTrigger>
          </TabsList>
          <TabsContent value="spans"><TraceWaterfall org={org} site={site} trace={trace.data} /></TabsContent>
          <TabsContent value="logs">
            <TraceLogs org={org} site={site} siteId={siteId} traceId={trace.data.trace_id} timezone={timezone} around={trace.data.spans[0]?.start} />
          </TabsContent>
        </Tabs>
      ) : null}
    </PageContainer>
  )
}
