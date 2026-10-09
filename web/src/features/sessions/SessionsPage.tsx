import { ListPager } from '@/components/ListPager'
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
import { ReplayLink } from '@/components/replay/ReplayLink'
import { ReplayOverlay, type ReplayOverlayRow } from '@/components/replay/ReplayOverlay'
import { BrowserLabel } from '@/components/visitor/BrowserLabel'
import { DeviceLabel } from '@/components/visitor/DeviceLabel'
import { Location } from '@/components/visitor/Location'
import { OsLabel } from '@/components/visitor/OsLabel'
import type { Filter } from '@/lib/search'

import { useSession, useSessions } from './api'
import { TimelineItem } from './TimelineItem'
import type { SessionSummary } from './types'

type Paged<T> = T & Partial<{ total: number; total_capped: boolean }>

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

export function SessionsPage({
  siteId,
  dateRange,
  filters,
  cursor,
  page,
  selectedSessionId,
  onFirst,
  onPrevious,
  onNext,
  onSelect,
  hasReplayOnly = false,
  org,
  site,
  replayId,
  onReplayChange,
}: {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
  cursor?: string
  page: number
  selectedSessionId?: string
  onFirst: () => void
  onPrevious: () => void
  onNext: (cursor: string) => void
  onSelect: (sessionId?: string) => void
  hasReplayOnly?: boolean
  org?: string
  site?: string
  replayId?: string
  onReplayChange?: (sessionId?: string, options?: { replace?: boolean }) => void
}) {
  const sessions = useSessions(siteId, dateRange, filters, cursor, hasReplayOnly)
  const detail = useSession(siteId, selectedSessionId)
  const data = sessions.data as Paged<NonNullable<typeof sessions.data>> | undefined
  const replayRows: ReplayOverlayRow[] = sessions.data?.data
    .filter((session) => session.has_replay)
    .map((session) => ({
      sessionId: session.id,
      startedAt: session.started_at,
      browser: session.browser,
      os: session.os,
      device: session.device,
      city: session.city,
      country: session.country,
      durationMs: session.duration_ms,
      actions: session.pageviews + session.events,
      pages: session.pageviews,
    })) ?? []

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
                  <TableHead>Replay</TableHead>
                  <TableHead>Entry → exit</TableHead>
                  <TableHead>Source</TableHead>
                  <TableHead>Location</TableHead>
                  <TableHead>Browser</TableHead>
                  <TableHead>OS</TableHead>
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
                    <TableCell>{org && site ? <ReplayLink hasReplay={(session as SessionSummary & { has_replay?: boolean }).has_replay} org={org} site={site} sessionId={session.id} onOpen={onReplayChange ? (id) => onReplayChange(id) : undefined} /> : null}</TableCell>
                    <TableCell>{session.entry_page} → {session.exit_page}</TableCell>
                    <TableCell>{session.source || 'Direct'}</TableCell>
                    <TableCell><Location city={session.city} country={session.country} /></TableCell>
                    <TableCell><BrowserLabel value={session.browser} /></TableCell>
                    <TableCell><OsLabel value={session.os} /></TableCell>
                    <TableCell><DeviceLabel value={session.device} /></TableCell>
                    <TableCell>{session.external_id ?? 'Anonymous'}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatDuration(session.duration_ms)}</TableCell>
                    <TableCell className="text-right tabular-nums">{session.pageviews} / {session.events}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </section>
      )}

      {!sessions.isPending && !sessions.isError ? (
        <ListPager
          fetching={sessions.isFetching}
          hasNext={Boolean(data?.next_cursor)}
          page={page}
          pageSize={50}
          rowCount={data?.data.length ?? 0}
          total={data?.total}
          totalCapped={data?.total_capped}
          onFirst={onFirst}
          onNext={() => data?.next_cursor && onNext(data.next_cursor)}
          onPrevious={onPrevious}
        />
      ) : null}

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
      {org && site && onReplayChange ? (
        <ReplayOverlay
          open={Boolean(replayId)}
          org={org}
          rows={replayRows}
          sessionId={replayId}
          site={site}
          siteId={siteId}
          onOpenChange={(open) => !open && onReplayChange(undefined, { replace: true })}
          onSessionChange={(id) => onReplayChange(id, { replace: true })}
        />
      ) : null}
    </main>
  )
}
