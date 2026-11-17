import { Link } from '@tanstack/react-router'
import { useState } from 'react'
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { copyText } from '@/lib/clipboard'
import { fmt } from '@/lib/format'

import { useAppIssue, useSetIssueStatus, useUpdateIssue, type AppIssueDetailData } from './api'
import { IssueActivity } from './IssueActivity'
import { IssueComments } from './IssueComments'
import { IssueFeedback } from './IssueFeedback'
import { IssueWorkflow } from './IssueWorkflow'
import { formatDay } from './format'
import type { AppSearch } from './search'
import { StatusBadge } from './IssuesTable'

const DIMENSIONS: [string, string][] = [
  ['release', 'Release'], ['platform', 'Platform'], ['os_version', 'OS version'], ['arch', 'Architecture'], ['device_class', 'Device class'], ['environment', 'Environment'],
]

function BarList({ title, rows }: { title: string; rows: AppIssueDetailData['breakdowns'][string] }) {
  const max = Math.max(...rows.map((row) => row.events), 1)
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">{title}</CardTitle></CardHeader>
      <CardContent>
        <ol className="flex flex-col gap-2">
          {rows.map((row) => (
            <li key={row.value} className="relative flex justify-between gap-3 overflow-hidden rounded px-2 py-1 text-sm">
              <span aria-hidden className="absolute inset-y-0 left-0 bg-chart-2/20" style={{ width: `${(row.events / max) * 100}%` }} />
              <span className="relative truncate">{row.value}</span>
              <span className="relative tabular-nums text-muted-foreground">{fmt.number(row.events)}</span>
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  )
}

function EventRow({ event, org, site, issueId }: { event: AppIssueDetailData['latest_events'][number]; org: string; site: string; issueId: number }) {
  const [open, setOpen] = useState(false)
  const label = `${event.ts.slice(0, 19).replace('T', ' ')} · ${event.release || 'no release'} · ${event.platform}${event.os_version ? ` ${event.os_version}` : ''}`
  return (
    <li className="border-b last:border-0">
      <button type="button" aria-expanded={open} className="flex w-full items-center justify-between gap-3 py-2 text-left text-sm" onClick={() => setOpen(!open)}>
        <span>{label}</span>
        <span aria-hidden className="text-muted-foreground">{open ? '−' : '+'}</span>
      </button>
      {open ? (
        <div className="flex flex-col gap-2 pb-3">
          <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-muted-foreground md:grid-cols-4">
            {([['Arch', event.arch], ['Device', event.device_class], ['Runtime', event.runtime], ['Signal', event.signal], ['Exit reason', event.exit_reason]] as const).filter(([, value]) => value).map(([name, value]) => (
              <div key={name}><dt className="inline">{name}: </dt><dd className="inline text-foreground">{value}</dd></div>
            ))}
          </dl>
          {event.frames ? (
            <>
              <pre className="max-h-80 overflow-auto rounded-lg bg-muted p-3 font-mono text-xs"><code>{event.frames}</code></pre>
              <div><Button type="button" size="sm" variant="outline" onClick={() => void copyText(event.frames)}>Copy frames</Button></div>
            </>
          ) : <p className="text-xs text-muted-foreground">No frames were reported.</p>}
          {event.event_id ? (
            <div><Link className="text-sm font-medium underline" to="/app/$org/$site/issues/$issue/events/$event" params={{ org, site, issue: String(issueId), event: event.event_id }}>Full event detail</Link></div>
          ) : null}
        </div>
      ) : null}
    </li>
  )
}

export function IssueDetail({ org, site, siteId, issueId, timezone, search, canEdit }: {
  org: string; site: string; siteId: number; issueId: number; timezone: string; search: AppSearch; canEdit: boolean
}) {
  const issue = useAppIssue(siteId, issueId, search, timezone)
  const setStatus = useSetIssueStatus(siteId, issueId)
  const archive = useUpdateIssue(siteId, issueId)
  if (issue.isPending) return <PageContainer><PageHeader title="Issue" /><p role="status" className="text-sm text-muted-foreground">Loading issue…</p></PageContainer>
  if (issue.isError) return <PageContainer><PageHeader title="Issue" /><p role="alert" className="text-sm text-destructive">{issue.error.message}</p></PageContainer>
  const data = issue.data
  const closed = data.status === 'resolved' || data.status === 'ignored'
  const archived = data.status === 'archived'
  const act = (status: 'open' | 'resolved' | 'ignored', resolved_in_release?: string) => setStatus.mutate(resolved_in_release ? { status, resolved_in_release } : { status })
  const breakdowns = [
    ...DIMENSIONS.map(([key, title]) => [title, data.breakdowns[key]] as const),
    ...Object.keys(data.breakdowns).filter((key) => key.startsWith('tag:')).sort().map((key) => [key.slice(4), data.breakdowns[key]] as const),
  ].filter(([, rows]) => rows && rows.length > 0)
  return (
    <PageContainer>
      <PageHeader
        title={data.title}
        description={
          <span className="flex flex-wrap items-center gap-2">
            <Badge variant="outline">{data.kind}</Badge>
            <StatusBadge status={data.status} />
            <span>{data.error_type}</span>
            <span>· {fmt.number(data.event_count)} events · first {formatDay(data.first_seen)}, last {formatDay(data.last_seen)}</span>
            {data.resolved_next ? <span>· resolved in the next release</span> : null}
            {data.resolved_in_release ? <span>· resolved in {data.resolved_in_release}</span> : null}
          </span>
        }
        actions={canEdit ? (
          <div className="flex flex-wrap gap-2">
            {archived
              ? <Button variant="outline" onClick={() => archive.mutate({ archive: false })}>Unarchive</Button>
              : closed
                ? <Button variant="outline" onClick={() => act('open')}>Reopen</Button>
                : (
                  <>
                    <Button onClick={() => act('resolved')}>Resolve</Button>
                    <Button variant="outline" onClick={() => act('resolved', 'next')}>Resolve in next release</Button>
                    <Button variant="outline" onClick={() => act('ignored')}>Ignore</Button>
                    <Button variant="outline" onClick={() => archive.mutate({ archive: true })}>Archive until escalating</Button>
                  </>
                )}
          </div>
        ) : undefined}
      />
      <Link className="text-sm font-medium underline" to="/app/$org/$site/issues" params={{ org, site }}>All issues</Link>
      {setStatus.isError ? <p role="alert" className="text-sm text-destructive">{setStatus.error.message}</p> : null}
      {archive.isError ? <p role="alert" className="text-sm text-destructive">{archive.error.message}</p> : null}
      <IssueWorkflow siteId={siteId} issue={data} canEdit={canEdit} />
      <Card>
        <CardHeader><CardTitle>Events per day</CardTitle><CardDescription>Occurrences of this issue in the selected period.</CardDescription></CardHeader>
        <CardContent>
          {data.series.length === 0 ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No events in this period.</p> : (
            <div className="h-56" role="img" aria-label="Daily events for this issue">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart accessibilityLayer data={data.series}>
                  <CartesianGrid vertical={false} strokeDasharray="3 3" />
                  <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
                  <YAxis allowDecimals={false} tickLine={false} axisLine={false} width={36} />
                  <Tooltip />
                  <Area dataKey="events" name="Events" fill="var(--chart-1)" stroke="var(--chart-1)" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          )}
        </CardContent>
      </Card>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {breakdowns.map(([title, rows]) => <BarList key={title} title={title} rows={rows} />)}
      </div>
      <Card>
        <CardHeader><CardTitle>Latest events</CardTitle><CardDescription>Scrubbed stack frames; no personal identifiers are stored.</CardDescription></CardHeader>
        <CardContent>
          {data.latest_events.length === 0 ? <p className="text-sm text-muted-foreground">No events in this period.</p> : <ul>{data.latest_events.map((event) => <EventRow key={event.ts + event.release} event={event} org={org} site={site} issueId={issueId} />)}</ul>}
        </CardContent>
      </Card>
      <IssueFeedback siteId={siteId} issueId={issueId} />
      <IssueComments siteId={siteId} issueId={issueId} canEdit={canEdit} />
      <IssueActivity siteId={siteId} issueId={issueId} />
    </PageContainer>
  )
}
