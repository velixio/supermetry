import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ErrorState } from '@/components/ErrorState'
import type { Filter } from '@/lib/search'

import { useSession, useSessions } from './api'
import type { SessionSummary, SessionTimelineItem } from './types'

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function formatDuration(milliseconds: number): string {
  if (milliseconds < 1000) return '<1s'
  const seconds = Math.round(milliseconds / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remainder = seconds % 60
  return `${minutes}m ${remainder}s`
}

function locationLabel(session: SessionSummary): string {
  return [session.city, session.country].filter(Boolean).join(', ') || 'Unknown'
}

function deviceLabel(session: SessionSummary): string {
  return [session.device, session.browser, session.os].filter(Boolean).join(' · ') || 'Unknown'
}

function TimelineItem({ item }: { item: SessionTimelineItem }) {
  const props = item.props ? Object.entries(item.props) : []
  return (
    <li className="flex flex-col gap-1 border-l-2 border-border py-1 pl-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant={item.kind === 'pageview' ? 'secondary' : 'outline'}>{item.kind}</Badge>
        <span className="font-medium">{item.name}</span>
        <time className="text-xs text-muted-foreground" dateTime={item.ts}>{formatDate(item.ts)}</time>
      </div>
      <span className="text-sm text-muted-foreground">{item.page}</span>
      {props.map(([key, value]) => (
        <span className="text-xs text-muted-foreground" key={key}>{key}: {String(value)}</span>
      ))}
    </li>
  )
}

export function SessionsPage({
  siteId,
  dateRange,
  filters,
  cursor,
  selectedSessionId,
  onCursorChange,
  onSelect,
}: {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
  cursor?: string
  selectedSessionId?: string
  onCursorChange: (cursor?: string) => void
  onSelect: (sessionId?: string) => void
}) {
  const sessions = useSessions(siteId, dateRange, filters, cursor)
  const detail = useSession(siteId, selectedSessionId)

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Sessions</h1>
        <p className="text-sm text-muted-foreground">Inspect anonymous visits without linking people across days.</p>
      </header>
      {sessions.isError ? (
        <ErrorState detail={sessions.error?.detail} onRetry={() => void sessions.refetch()} />
      ) : sessions.isPending ? (
        <div aria-label="Loading sessions" className="flex flex-col gap-3">
          <Skeleton className="h-10 w-full" />
          <Skeleton className="h-52 w-full" />
        </div>
      ) : sessions.data.data.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>No sessions found</EmptyTitle>
            <EmptyDescription>Try a wider date range or remove a filter.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <section className="flex flex-col gap-4">
          <div className="overflow-x-auto rounded-xl border bg-card">
            <Table aria-label="Sessions">
              <TableHeader>
                <TableRow>
                  <TableHead>Started</TableHead>
                  <TableHead>Entry → exit</TableHead>
                  <TableHead>Source</TableHead>
                  <TableHead>Location</TableHead>
                  <TableHead>Device</TableHead>
                  <TableHead>Profile</TableHead>
                  <TableHead className="text-right">Duration</TableHead>
                  <TableHead className="text-right">Views / events</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sessions.data.data.map((session) => (
                  <TableRow key={`${session.started_at}:${session.id}`}>
                    <TableCell>
                      <Button aria-label={`View session ${session.id}`} className="h-auto p-0" variant="link" onClick={() => onSelect(session.id)}>
                        <time dateTime={session.started_at}>{formatDate(session.started_at)}</time>
                      </Button>
                    </TableCell>
                    <TableCell>{session.entry_page} → {session.exit_page}</TableCell>
                    <TableCell>{session.source || 'Direct'}</TableCell>
                    <TableCell>{locationLabel(session)}</TableCell>
                    <TableCell>{deviceLabel(session)}</TableCell>
                    <TableCell>{session.external_id ?? 'Anonymous'}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatDuration(session.duration_ms)}</TableCell>
                    <TableCell className="text-right tabular-nums">{session.pageviews} / {session.events}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="flex justify-end gap-2">
            <Button disabled={!cursor || sessions.isFetching} variant="outline" onClick={() => onCursorChange(undefined)}>First page</Button>
            <Button disabled={!sessions.data.next_cursor || sessions.isFetching} variant="outline" onClick={() => onCursorChange(sessions.data.next_cursor ?? undefined)}>Next page</Button>
          </div>
        </section>
      )}

      <Sheet open={Boolean(selectedSessionId)} onOpenChange={(open) => !open && onSelect(undefined)}>
        <SheetContent className="overflow-y-auto sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>Session timeline</SheetTitle>
            <SheetDescription>Anonymous session IDs reset daily and cannot link a visitor across days.</SheetDescription>
          </SheetHeader>
          {detail.isError ? (
            <div className="px-4"><ErrorState detail={detail.error?.detail} onRetry={() => void detail.refetch()} /></div>
          ) : detail.isPending ? (
            <div aria-label="Loading session timeline" className="flex flex-col gap-3 px-4">
              <Skeleton className="h-16 w-full" />
              <Skeleton className="h-16 w-full" />
            </div>
          ) : detail.data ? (
            <ol className="flex flex-col gap-4 px-4 pb-6">
              {detail.data.timeline.map((item, index) => <TimelineItem item={item} key={`${item.ts}:${index}`} />)}
            </ol>
          ) : null}
        </SheetContent>
      </Sheet>
    </main>
  )
}
