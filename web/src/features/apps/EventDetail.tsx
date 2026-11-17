import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

import { crumbTime, frameLabel, readBody, show, type BodyException, type BodyFrame } from './eventBody'

export type AppEventBody = components['schemas']['AppEventBody']

export function useAppEvent(siteId: number, eventId: string) {
  return useQuery({ queryKey: ['app-event', siteId, eventId], queryFn: () => privateRequest<AppEventBody>('GET', `/api/private/sites/${siteId}/app/events/${encodeURIComponent(eventId)}`) })
}

function Frame({ frame }: { frame: BodyFrame }) {
  const inApp = frame.in_app === true
  const context = frame.context_line
  return (
    <li data-in-app={inApp ? 'true' : 'false'} className={`rounded-md border-l-4 px-3 py-2 font-mono text-xs ${inApp ? 'border-l-chart-1 bg-chart-1/10' : 'border-l-transparent text-muted-foreground'}`}>
      <div className="flex flex-wrap items-center gap-2">
        <span className={inApp ? 'font-semibold text-foreground' : ''}>{frameLabel(frame)}</span>
        {frame.function ? <span>in {frame.function}</span> : null}
        {inApp ? <Badge variant="secondary">in app</Badge> : null}
      </div>
      {context ? (
        <pre className="mt-1 overflow-auto whitespace-pre text-foreground"><code>{[...(frame.pre_context ?? []), context, ...(frame.post_context ?? [])].map((line, i) => (
          <span key={i} className={line === context && i === (frame.pre_context ?? []).length ? 'font-semibold' : 'opacity-70'}>{line}{'\n'}</span>
        ))}</code></pre>
      ) : null}
    </li>
  )
}

function ExceptionBlock({ exception, index, total }: { exception: BodyException; index: number; total: number }) {
  const frames = [...(exception.frames ?? [])].reverse() // Sentry sends oldest first; show the crash site on top.
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2 text-base">
          {exception.type || 'Error'}
          {exception.mechanism?.handled === false ? <Badge variant="destructive">unhandled</Badge> : null}
          {exception.mechanism?.type ? <Badge variant="outline">{exception.mechanism.type}</Badge> : null}
        </CardTitle>
        <CardDescription>
          {exception.value || 'No message'}
          {total > 1 ? <span className="ml-2">· {index === 0 ? 'cause chain, outermost first' : `cause ${index}`}</span> : null}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {frames.length === 0 ? <p className="text-sm text-muted-foreground">No stack frames were reported.</p> : <ul className="flex flex-col gap-1" aria-label={`Stack frames for ${exception.type || 'error'}`}>{frames.map((frame, i) => <Frame key={i} frame={frame} />)}</ul>}
      </CardContent>
    </Card>
  )
}

function KeyValues({ entries }: { entries: [string, unknown][] }) {
  return (
    <dl className="grid grid-cols-[minmax(6rem,auto)_1fr] gap-x-4 gap-y-1 text-sm">
      {entries.map(([key, value]) => (
        <div key={key} className="contents"><dt className="text-muted-foreground">{key}</dt><dd className="min-w-0 break-words">{show(value)}</dd></div>
      ))}
    </dl>
  )
}

export function EventBodyView({ body }: { body: unknown }) {
  const view = readBody(body)
  const nothing = !view.message && view.exceptions.length === 0 && view.breadcrumbs.length === 0 && view.contexts.length === 0 && view.tags.length === 0
  return (
    <div className="flex min-w-0 flex-col gap-4">
      {view.message ? <Card><CardHeader><CardTitle className="text-base">Message</CardTitle></CardHeader><CardContent><p className="break-words text-sm">{view.message}</p></CardContent></Card> : null}
      {view.exceptions.map((exception, i) => <ExceptionBlock key={i} exception={exception} index={i} total={view.exceptions.length} />)}
      {view.breadcrumbs.length > 0 ? (
        <Card>
          <CardHeader><CardTitle className="text-base">Breadcrumbs</CardTitle><CardDescription>What happened before the event, oldest first.</CardDescription></CardHeader>
          <CardContent>
            <ol className="flex flex-col divide-y text-sm" aria-label="Breadcrumbs">
              {view.breadcrumbs.map((crumb, i) => (
                <li key={i} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-2">
                  <time className="font-mono text-xs text-muted-foreground">{crumbTime(crumb.timestamp)}</time>
                  {crumb.level ? <Badge variant={crumb.level === 'error' || crumb.level === 'fatal' ? 'destructive' : 'outline'}>{crumb.level}</Badge> : null}
                  <span className="font-medium">{crumb.category || crumb.type || 'default'}</span>
                  {crumb.message ? <span className="min-w-0 break-words text-muted-foreground">{crumb.message}</span> : null}
                  {crumb.data && Object.keys(crumb.data).length > 0 ? <code className="w-full break-all text-xs text-muted-foreground">{show(crumb.data)}</code> : null}
                </li>
              ))}
            </ol>
          </CardContent>
        </Card>
      ) : null}
      {view.contexts.length > 0 || view.request ? (
        <div className="grid gap-4 md:grid-cols-2">
          {view.contexts.map(([name, values]) => <Card key={name}><CardHeader><CardTitle className="text-base capitalize">{name}</CardTitle></CardHeader><CardContent><KeyValues entries={Object.entries(values).filter(([key]) => key !== 'type')} /></CardContent></Card>)}
          {view.request ? <Card><CardHeader><CardTitle className="text-base">Request</CardTitle></CardHeader><CardContent><KeyValues entries={[['method', view.request.method], ['url', view.request.url], ...Object.entries(view.request.headers ?? {})].filter(([, value]) => value) as [string, unknown][]} /></CardContent></Card> : null}
        </div>
      ) : null}
      {view.tags.length > 0 ? (
        <Card>
          <CardHeader><CardTitle className="text-base">Tags</CardTitle></CardHeader>
          <CardContent><ul className="flex flex-wrap gap-2" aria-label="Tags">{view.tags.map(([key, value]) => <li key={key}><Badge variant="outline">{key}: {show(value)}</Badge></li>)}</ul></CardContent>
        </Card>
      ) : null}
      {view.attachments.length > 0 ? (
        <Card><CardHeader><CardTitle className="text-base">Attachments</CardTitle><CardDescription>Names and sizes only; contents are not stored.</CardDescription></CardHeader>
          <CardContent><ul className="text-sm">{view.attachments.map((a, i) => <li key={i}>{a.name || 'attachment'} {a.size !== undefined ? <span className="text-muted-foreground">({a.size} bytes)</span> : null}</li>)}</ul></CardContent></Card>
      ) : null}
      {view.sdk?.name ? <p className="text-xs text-muted-foreground">Sent by {view.sdk.name}{view.sdk.version ? ` ${view.sdk.version}` : ''}</p> : null}
      {nothing ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">This event has no stored detail.</p> : null}
    </div>
  )
}

export function EventDetail({ org, site, siteId, issueId, eventId }: { org: string; site: string; siteId: number; issueId: number; eventId: string }) {
  const event = useAppEvent(siteId, eventId)
  const back = <Link className="text-sm font-medium underline" to="/app/$org/$site/issues/$issue" params={{ org, site, issue: String(issueId) }}>Back to issue</Link>
  if (event.isPending) return <PageContainer><PageHeader title="Event" /><p role="status" className="text-sm text-muted-foreground">Loading event…</p></PageContainer>
  if (event.isError) {
    return (
      <PageContainer><PageHeader title="Event" />{back}
        <p role="alert" className="text-sm text-destructive">Event detail is kept for 30 days, and this event is not available: {event.error.message}</p>
      </PageContainer>
    )
  }
  return (
    <PageContainer>
      <PageHeader title="Event" description={<span>{event.data.ts.slice(0, 19).replace('T', ' ')} · <span className="font-mono">{event.data.event_id}</span></span>} />
      {back}
      <EventBodyView body={event.data.body} />
    </PageContainer>
  )
}
