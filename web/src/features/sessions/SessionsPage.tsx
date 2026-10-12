import { useCapability } from '@/lib/authorization'
import { ListPager } from '@/components/ListPager'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from '@/components/ui/card'
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
import { en } from '@/messages/en'
import { PlayIcon } from 'lucide-react'

import { useSession, useSessions } from './api'
import { TimelineItem } from './TimelineItem'
import type { SessionSummary } from './types'
import { activeLocale } from '@/lib/i18n/intl'

type Paged<T> = T & Partial<{ total: number; total_capped: boolean }>

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(activeLocale(), { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function formatDuration(milliseconds: number): string {
  if (milliseconds < 1000) return '<1s'
  const seconds = Math.round(milliseconds / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  const remainder = seconds % 60
  return `${minutes}m ${remainder}s`
}

function formatCount(value: number, singular: string, plural: string): string {
  return `${value.toLocaleString()} ${value === 1 ? singular : plural}`
}

function SessionCard({
  session,
  canReadProfiles,
  canReplay,
  org,
  site,
  onReplayChange,
  onSelect,
}: {
  session: SessionSummary
  canReadProfiles: boolean
  canReplay: boolean
  org?: string
  site?: string
  onReplayChange?: (sessionId?: string, options?: { replace?: boolean }) => void
  onSelect: (sessionId?: string) => void
}) {
  const started = formatDate(session.started_at)
  const hasReplay = (session as SessionSummary & { has_replay?: boolean }).has_replay

  return (
    <li aria-label={`${en.sessions.card.sessionStarted} ${started}`}>
      <Card className="gap-3 py-3">
        <CardHeader className="flex items-start justify-between gap-2">
          <CardTitle>
            <Button
              aria-label={`View session ${session.id}`}
              className="min-h-11 justify-start px-0 text-left whitespace-normal"
              variant="link"
              onClick={() => onSelect(session.id)}
            >
              <time dateTime={session.started_at}>{started}</time>
            </Button>
          </CardTitle>
          {canReplay && org && site ? (
            onReplayChange && hasReplay ? (
              <Button aria-label="Play replay" className="size-11" size="icon" type="button" variant="ghost" onClick={() => onReplayChange(session.id)}>
                <PlayIcon />
              </Button>
            ) : (
              <div className="[&_[data-slot=button]]:size-11">
                <ReplayLink hasReplay={hasReplay} org={org} site={site} sessionId={session.id} />
              </div>
            )
          ) : null}
        </CardHeader>
        <CardContent className="flex min-w-0 flex-col gap-3 text-sm">
          <p className="[overflow-wrap:anywhere] font-medium">{session.entry_page} → {session.exit_page}</p>
          <Location city={session.city} country={session.country} />
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <BrowserLabel value={session.browser} />
            <OsLabel value={session.os} />
            <DeviceLabel value={session.device} />
          </div>
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs">
            <dt className="text-muted-foreground">{en.sessions.card.source}</dt>
            <dd className="[overflow-wrap:anywhere]">{session.source || 'Direct'}</dd>
            <dt className="text-muted-foreground">{en.sessions.card.profile}</dt>
            <dd className="[overflow-wrap:anywhere]">{(canReadProfiles ? session.external_id : undefined) ?? 'Anonymous'}</dd>
          </dl>
        </CardContent>
        <CardFooter className="flex-wrap gap-x-4 gap-y-2 text-xs text-muted-foreground tabular-nums">
          <span>{formatDuration(session.duration_ms)}</span>
          <span>{formatCount(session.pageviews, en.sessions.card.page, en.sessions.card.pages)}</span>
          <span>{formatCount(session.events, en.sessions.card.event, en.sessions.card.events)}</span>
        </CardFooter>
      </Card>
    </li>
  )
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
  const canReplay = useCapability('replay.read')
  const canReadProfiles = useCapability('profiles.read')
  const sessions = useSessions(siteId, dateRange, filters, cursor, canReplay && hasReplayOnly)
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
    <main className="flex min-w-0 max-w-full flex-col gap-6">
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
        <section className="flex min-w-0 max-w-full flex-col gap-4">
          <ol aria-label="Sessions" className="flex flex-col gap-3 md:hidden">
            {sessions.data.data.map((session) => (
              <SessionCard
                key={`${session.started_at}:${session.id}`}
                canReadProfiles={canReadProfiles}
                canReplay={canReplay}
                onReplayChange={onReplayChange}
                onSelect={onSelect}
                org={org}
                session={session}
                site={site}
              />
            ))}
          </ol>
          <div className="hidden min-w-0 max-w-full overflow-hidden rounded-xl border bg-card md:block">
            <Table aria-label="Sessions" className="min-w-[680px] lg:min-w-[980px] xl:min-w-[1450px]">
              <TableHeader>
                <TableRow>
                  <TableHead className="sticky left-0 bg-card after:absolute after:inset-y-0 after:right-0 after:w-px after:bg-border">Started</TableHead>
                  {canReplay ? <TableHead>Replay</TableHead> : null}
                  <TableHead>Entry → exit</TableHead>
                  <TableHead className="hidden xl:table-cell">Source</TableHead>
                  <TableHead className="hidden lg:table-cell">Location</TableHead>
                  <TableHead className="hidden lg:table-cell">Browser</TableHead>
                  <TableHead className="hidden lg:table-cell">OS</TableHead>
                  <TableHead className="hidden xl:table-cell">Device</TableHead>
                  <TableHead className="hidden xl:table-cell">Profile</TableHead>
                  <TableHead className="text-right">Duration</TableHead>
                  <TableHead className="text-right">Views / events</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sessions.data.data.map((session) => (
                  <TableRow key={`${session.started_at}:${session.id}`}>
                    <TableCell className="sticky left-0 bg-card after:absolute after:inset-y-0 after:right-0 after:w-px after:bg-border">
                      <Button aria-label={`View session ${session.id}`} className="h-auto p-0" variant="link" onClick={() => onSelect(session.id)}>
                        <time dateTime={session.started_at}>{formatDate(session.started_at)}</time>
                      </Button>
                    </TableCell>
                    {canReplay ? <TableCell>{org && site ? <ReplayLink hasReplay={(session as SessionSummary & { has_replay?: boolean }).has_replay} org={org} site={site} sessionId={session.id} onOpen={onReplayChange ? (id) => onReplayChange(id) : undefined} /> : null}</TableCell> : null}
                    <TableCell>{session.entry_page} → {session.exit_page}</TableCell>
                    <TableCell className="hidden xl:table-cell">{session.source || 'Direct'}</TableCell>
                    <TableCell className="hidden lg:table-cell"><Location city={session.city} country={session.country} /></TableCell>
                    <TableCell className="hidden lg:table-cell"><BrowserLabel value={session.browser} /></TableCell>
                    <TableCell className="hidden lg:table-cell"><OsLabel value={session.os} /></TableCell>
                    <TableCell className="hidden xl:table-cell"><DeviceLabel value={session.device} /></TableCell>
                    <TableCell className="hidden xl:table-cell">{(canReadProfiles ? session.external_id : undefined) ?? 'Anonymous'}</TableCell>
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
      {canReplay && org && site && onReplayChange ? (
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
