import { Skeleton } from '@/components/ui/skeleton'
import { obsLogs as t } from '@/messages/obsLogs'

import { useTraceLogs } from './api'
import { LogRow } from './LogRow'

/** The logs of one trace, oldest first, for the trace view's Logs tab. */
export function TraceLogs({ org, site, siteId, traceId, timezone, around }: { org: string; site: string; siteId: number; traceId: string; timezone: string; around?: string }) {
  const logs = useTraceLogs(siteId, traceId, around)
  if (logs.isPending) return <Skeleton className="h-24 w-full" aria-label={t.loading} />
  if (logs.isError) return <p role="alert" className="text-sm text-destructive">{logs.error.message}</p>
  if (logs.data.length === 0) return <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">{t.traceLogsEmpty}</p>
  return (
    <ul className="rounded-lg border bg-card" aria-label={t.traceLogs}>
      {logs.data.map((log, index) => <LogRow key={`${log.ts}-${index}`} log={log} org={org} site={site} timezone={timezone} showTraceLink={false} />)}
    </ul>
  )
}
