import type { ComponentProps } from 'react'
import { useCapability } from '@/lib/authorization'
import { Link } from '@tanstack/react-router'
import { useCallback, useMemo, useState } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Skeleton } from '@/components/ui/skeleton'
import { BrowserLabel } from '@/components/visitor/BrowserLabel'
import { DeviceLabel } from '@/components/visitor/DeviceLabel'
import { Location } from '@/components/visitor/Location'
import { OsLabel } from '@/components/visitor/OsLabel'
import { TimelineItem } from '@/features/sessions/TimelineItem'
import { useSession } from '@/features/sessions/api'
import { getReplayCSS, getReplayData } from '@/lib/replay-api'
import { en } from '@/messages/en'

import { ReplayPlayer, type MountPlayer, type ReplayPlayerHandle } from './ReplayPlayer'

export type ReplayOverlayRow = {
  sessionId: string
  startedAt?: string
  browser?: string
  os?: string
  device?: string
  city?: string
  country?: string
  durationMs?: number
  actions?: number
  pages?: number
}

function formatDate(value?: string): string {
  if (!value) return 'Start time unavailable'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function formatDuration(milliseconds?: number): string {
  if (milliseconds === undefined) return '—'
  const seconds = Math.round(milliseconds / 1000)
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}

function ReplayOverlayContent({
  open,
  org,
  rows,
  sessionId,
  site,
  siteId,
  mountPlayer,
  onOpenChange,
  onSessionChange,
}: {
  open: boolean
  org: string
  rows: ReplayOverlayRow[]
  sessionId?: string
  site: string
  siteId: number
  mountPlayer?: MountPlayer
  onOpenChange: (open: boolean) => void
  onSessionChange: (sessionId: string) => void
}) {
  const detail = useSession(siteId, open ? sessionId : undefined)
  const [player, setPlayer] = useState<ReplayPlayerHandle | null>(null)
  const currentIndex = sessionId ? rows.findIndex((row) => row.sessionId === sessionId) : -1
  const row = currentIndex >= 0 ? rows[currentIndex] : undefined
  const session = detail.data?.session
  const timeline = detail.data?.timeline ?? []
  const firstTimestamp = timeline.length > 0 ? new Date(timeline[0].ts).getTime() : undefined
  const load = useCallback(
    (signal: AbortSignal) => getReplayData(siteId, sessionId!, signal),
    [sessionId, siteId],
  )
  const fetchCSS = useCallback((hash: string) => getReplayCSS(siteId, hash), [siteId])
  const handleReady = useCallback((next: ReplayPlayerHandle | null) => setPlayer(next), [])
  const search = useMemo(() => ({
    period: '30d' as const,
    compare: 'off' as const,
    f: [],
    metric: 'visitors' as const,
    include_imported: true,
  }), [])

  const startedAt = row?.startedAt ?? session?.started_at
  const durationMs = row?.durationMs ?? session?.duration_ms
  const actions = row?.actions ?? (session ? session.pageviews + session.events : undefined)
  const pages = row?.pages ?? session?.pageviews

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex h-[90vh] w-[95vw] max-w-[95vw] flex-col gap-0 overflow-hidden p-0 sm:max-w-[95vw]">
        <DialogHeader className="shrink-0 border-b px-5 py-4 pr-12">
          <DialogTitle>Session replay</DialogTitle>
          <DialogDescription>{formatDate(startedAt)}</DialogDescription>
        </DialogHeader>

        <div className="flex min-h-0 flex-1 flex-col overflow-y-auto lg:flex-row lg:overflow-hidden">
          <section aria-label="Replay player" className="flex h-[min(60dvh,32rem)] min-h-88 min-w-0 shrink-0 lg:h-auto lg:min-h-0 lg:flex-1 lg:overflow-hidden">
            {sessionId ? (
              <ReplayPlayer
                key={sessionId}
                fetchCSS={fetchCSS}
                load={load}
                mountPlayer={mountPlayer}
                onReady={handleReady}
              />
            ) : null}
          </section>

          <aside aria-label={en.a11y2.replayDetails} className="flex w-full shrink-0 flex-col gap-6 border-t p-4 lg:min-h-0 lg:w-80 lg:overflow-y-auto lg:border-t-0 lg:border-l">
            <section className="flex flex-col gap-3" aria-labelledby="replay-visitor-heading">
              <h2 className="font-medium" id="replay-visitor-heading">Visitor details</h2>
              <dl className="grid grid-cols-[auto_1fr] items-center gap-x-3 gap-y-2 text-sm">
                <dt className="text-muted-foreground">Location</dt><dd><Location city={row?.city ?? session?.city} country={row?.country ?? session?.country} /></dd>
                <dt className="text-muted-foreground">Browser</dt><dd><BrowserLabel value={row?.browser ?? session?.browser} /></dd>
                <dt className="text-muted-foreground">OS</dt><dd><OsLabel value={row?.os ?? session?.os} /></dd>
                <dt className="text-muted-foreground">Device</dt><dd><DeviceLabel value={row?.device ?? session?.device} /></dd>
                <dt className="text-muted-foreground">Duration</dt><dd className="tabular-nums">{formatDuration(durationMs)}</dd>
                <dt className="text-muted-foreground">Actions</dt><dd className="tabular-nums">{actions ?? '—'}</dd>
                <dt className="text-muted-foreground">Pages</dt><dd className="tabular-nums">{pages ?? '—'}</dd>
              </dl>
            </section>

            <section className="flex flex-col gap-3" aria-labelledby="replay-timeline-heading">
              <h2 className="font-medium" id="replay-timeline-heading">Session timeline</h2>
              {detail.isPending ? (
                <div className="flex flex-col gap-2" aria-label="Loading session timeline"><Skeleton className="h-16 w-full" /><Skeleton className="h-16 w-full" /></div>
              ) : detail.isError ? (
                <p className="text-sm text-destructive" role="alert">Could not load the session timeline.</p>
              ) : timeline.length === 0 ? (
                <p className="text-sm text-muted-foreground">No retained timeline events.</p>
              ) : (
                <ol className="flex flex-col gap-4">
                  {timeline.map((item, index) => (
                    <TimelineItem
                      item={item}
                      key={`${item.ts}:${index}`}
                      onClick={() => {
                        const timestamp = new Date(item.ts).getTime()
                        if (player && firstTimestamp !== undefined && Number.isFinite(timestamp)) {
                          player.goto(Math.max(0, timestamp - firstTimestamp))
                        }
                      }}
                    />
                  ))}
                </ol>
              )}
            </section>
          </aside>
        </div>

        <DialogFooter className="m-0 shrink-0 rounded-none px-4 py-3">
          <div className="flex flex-1 gap-2">
            <Button disabled={currentIndex <= 0} type="button" variant="outline" onClick={() => onSessionChange(rows[currentIndex - 1].sessionId)}>Previous replay</Button>
            <Button disabled={currentIndex < 0 || currentIndex >= rows.length - 1} type="button" variant="outline" onClick={() => onSessionChange(rows[currentIndex + 1].sessionId)}>Next replay</Button>
          </div>
          {sessionId ? (
            <Button asChild variant="outline">
              <Link params={{ org, site, id: sessionId }} search={search} to="/app/$org/$site/replays/$id">Open in new page</Link>
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function ReplayOverlay(props: ComponentProps<typeof ReplayOverlayContent>) {
  const allowed = useCapability('replay.read')
  if (!allowed) return null
  return <ReplayOverlayContent {...props} />
}
