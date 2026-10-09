import type { ComponentProps } from 'react'
import { useCapability, AccessDenied } from '@/lib/authorization'
import { ReplayLink } from './ReplayLink'
import { ListPager } from '@/components/ListPager'
import { ReplayOverlay, type ReplayOverlayRow } from './ReplayOverlay'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { BrowserLabel } from '@/components/visitor/BrowserLabel'
import { DeviceLabel } from '@/components/visitor/DeviceLabel'
import { Location } from '@/components/visitor/Location'
import { OsLabel } from '@/components/visitor/OsLabel'
import { useReplays } from './api'
import type { ReplayListItem } from '@/lib/replay-api'

type ReplayRow = ReplayListItem & Partial<{
  actions: number
  browser: string
  city: string
  country: string
  device: string
  os: string
}>

type Paged<T> = T & Partial<{ total: number; total_capped: boolean }>

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`
  return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`
}

function ReplaysPageContent({
  siteId,
  org,
  site,
  from,
  to,
  cursor,
  page,
  replayId,
  onFirst,
  onPrevious,
  onNext,
  onReplayChange,
}: {
  siteId: number
  org: string
  site: string
  from: string
  to: string
  cursor?: string
  page: number
  replayId?: string
  onFirst: () => void
  onPrevious: () => void
  onNext: (cursor: string) => void
  onReplayChange?: (sessionId?: string, options?: { replace?: boolean }) => void
}) {
  const replays = useReplays(siteId, from, to, cursor)
  const data = replays.data as Paged<NonNullable<typeof replays.data>> | undefined
  const replayRows: ReplayOverlayRow[] = (replays.data?.items as ReplayRow[] | undefined)?.map((replay) => ({
    sessionId: replay.session_id,
    startedAt: replay.started_at,
    browser: replay.browser,
    os: replay.os,
    device: replay.device,
    city: replay.city,
    country: replay.country,
    durationMs: replay.duration_s * 1000,
    actions: replay.actions,
    pages: replay.pages,
  })) ?? []
  return (
    <main className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Replays</h1>
        <p className="text-sm text-muted-foreground">Watch sampled sessions that matched your recording rules.</p>
      </header>
      {replays.isPending ? (
        <Skeleton aria-label="Loading replays" className="h-64 w-full" />
      ) : replays.isError ? (
        <p role="alert" className="text-sm text-destructive">{replays.error.message}</p>
      ) : replays.data.items.length === 0 ? (
        <Empty><EmptyHeader><EmptyTitle>No replays found</EmptyTitle><EmptyDescription>Try a wider date range or adjust replay sampling.</EmptyDescription></EmptyHeader></Empty>
      ) : (
        <div className="overflow-x-auto rounded-xl border bg-card">
          <Table aria-label="Session replays">
            <TableHeader><TableRow>
              <TableHead aria-label="Play"><span aria-hidden="true">▶</span></TableHead>
              <TableHead>Started</TableHead><TableHead>Location</TableHead><TableHead>Browser</TableHead>
              <TableHead>OS</TableHead><TableHead>Device</TableHead><TableHead>Actions</TableHead>
              <TableHead>Duration</TableHead><TableHead>Pages</TableHead><TableHead>Trigger</TableHead>
            </TableRow></TableHeader>
            <TableBody>{(replays.data.items as ReplayRow[]).map((replay) => (
              <TableRow key={replay.session_id}>
                <TableCell><ReplayLink hasReplay org={org} site={site} sessionId={replay.session_id} onOpen={onReplayChange ? (id) => onReplayChange(id) : undefined} /></TableCell>
                <TableCell><time dateTime={replay.started_at}>{new Date(replay.started_at).toLocaleString()}</time></TableCell>
                <TableCell><Location city={replay.city} country={replay.country} /></TableCell>
                <TableCell><BrowserLabel value={replay.browser} /></TableCell>
                <TableCell><OsLabel value={replay.os} /></TableCell>
                <TableCell><DeviceLabel value={replay.device} /></TableCell>
                <TableCell className="tabular-nums">{replay.actions ?? 0}</TableCell>
                <TableCell className="tabular-nums">{formatDuration(replay.duration_s)}</TableCell>
                <TableCell className="tabular-nums">{replay.pages}</TableCell>
                <TableCell>{replay.trigger}</TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
        </div>
      )}
      {!replays.isPending && !replays.isError ? (
        <ListPager
          fetching={replays.isFetching}
          hasNext={Boolean(data?.next_cursor)}
          page={page}
          pageSize={50}
          rowCount={data?.items.length ?? 0}
          total={data?.total}
          totalCapped={data?.total_capped}
          onFirst={onFirst}
          onNext={() => data?.next_cursor && onNext(data.next_cursor)}
          onPrevious={onPrevious}
        />
      ) : null}
      {onReplayChange ? (
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

export function ReplaysPage(props: ComponentProps<typeof ReplaysPageContent>) {
  const allowed = useCapability('replay.read')
  if (!allowed) return <AccessDenied subject="watch replays" />
  return <ReplaysPageContent {...props} />
}
