import type { ComponentProps } from 'react'
import { useCapability, AccessDenied } from '@/lib/authorization'
import { ErrorState } from '@/components/ErrorState'
import { ListPager } from '@/components/ListPager'
import { ReplayLink } from '@/components/replay/ReplayLink'
import { ReplayOverlay, type ReplayOverlayRow } from '@/components/replay/ReplayOverlay'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { Role } from '@/components/Sidebar'

import { useDeleteProfile, useProfile, useProfileErasure, useRetryProfileErasure, type ErasureStatus } from './api'

type Paged<T> = T & Partial<{ total: number; total_capped: boolean }>

function displayValue(value: unknown): string {
  return typeof value === 'string' ? value : JSON.stringify(value)
}

function ProfileErasureState({
  erasureId,
  lastError,
  onDeleted,
  onRetry,
  retrying,
  status,
}: {
  erasureId: number
  lastError?: string | null
  onDeleted: () => void
  onRetry: (id: number) => void
  retrying: boolean
  status: ErasureStatus
}) {
  const failed = status === 'failed'
  const done = status === 'done'
  return (
    <Card>
      <CardHeader>
        <CardTitle>Profile deletion</CardTitle>
        <CardDescription>The erasure request is processed in the background and remains safe across restarts.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col items-start gap-3">
        <Badge variant={failed ? 'destructive' : done ? 'outline' : 'secondary'}>
          {failed ? 'Failed' : done ? 'Deleted' : 'Deletion in progress'}
        </Badge>
        {failed && lastError ? <p className="text-sm text-muted-foreground">{lastError}</p> : null}
        {failed ? <Button disabled={retrying} onClick={() => onRetry(erasureId)}>Retry</Button> : null}
        {done ? <Button variant="outline" onClick={onDeleted}>Back to profiles</Button> : null}
      </CardContent>
    </Card>
  )
}

function ProfileDetailPageContent({
  profileId,
  siteId,
  orgId,
  role,
  onDeleted,
  org,
  site,
  cursor,
  page,
  replayId,
  onFirst,
  onPrevious,
  onNext,
  onReplayChange,
}: {
  profileId: number
  siteId: number
  orgId: number
  role: Role
  onDeleted: () => void
  org?: string
  site?: string
  cursor?: string
  page: number
  replayId?: string
  onFirst: () => void
  onPrevious: () => void
  onNext: (cursor: string) => void
  onReplayChange?: (sessionId?: string, options?: { replace?: boolean }) => void
}) {
  const canReplay = useCapability('replay.read')
  const profile = useProfile(profileId, orgId, siteId, cursor)
  const remove = useDeleteProfile(siteId, orgId)
  const erasureId = remove.data?.erasure_id
  const erasure = useProfileErasure(erasureId, orgId)
  const retryErasure = useRetryProfileErasure(orgId)
  const canDelete = role === 'owner' || role === 'admin'

  if (profile.isError) return <ErrorState detail={profile.error?.message} onRetry={() => void profile.refetch?.()} />
  if (profile.isPending || !profile.data) return <Skeleton className="h-72 w-full" />
  if (profile.data.site_id !== siteId) return <AccessDenied subject="read this profile from the selected site" />
  const data = profile.data as Paged<typeof profile.data>
  const erasureStatus = erasure.data?.status ?? remove.data?.status

  if (erasureId && erasureStatus) {
    return (
      <div className="flex min-w-0 flex-col gap-6">
        <header className="flex min-w-0 flex-col gap-1">
          <p className="text-sm text-muted-foreground">Profile</p>
          <h1 className="truncate text-2xl font-semibold tracking-tight">{profile.data.external_id}</h1>
        </header>
        <ProfileErasureState
          erasureId={erasureId}
          lastError={erasure.data?.last_error}
          retrying={retryErasure.isPending}
          status={erasureStatus}
          onDeleted={onDeleted}
          onRetry={(id) => retryErasure.mutate(id)}
        />
      </div>
    )
  }

  const replayRows: ReplayOverlayRow[] = []
  const replayIDs = new Set<string>()
  for (const event of profile.data.timeline) {
    if (!event.has_replay || replayIDs.has(event.session_id)) continue
    replayIDs.add(event.session_id)
    replayRows.push({ sessionId: event.session_id })
  }
  const firstReplayId = replayRows[0]?.sessionId

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <header className="flex min-w-0 flex-col gap-1">
          <p className="text-sm text-muted-foreground">Profile</p>
          <h1 className="truncate text-2xl font-semibold tracking-tight">{profile.data.external_id}</h1>
        </header>
        {org && site ? <ReplayLink hasReplay={Boolean(firstReplayId)} org={org} site={site} sessionId={firstReplayId} onOpen={onReplayChange ? (id) => onReplayChange(id) : undefined} /> : null}
        {canDelete ? (
          <AlertDialog>
            <AlertDialogTrigger asChild><Button variant="destructive">Delete profile</Button></AlertDialogTrigger>
            <AlertDialogContent>
              <AlertDialogHeader>
                <AlertDialogTitle>Delete {profile.data.external_id}?</AlertDialogTitle>
                <AlertDialogDescription>This permanently erases this profile and its events. Historical aggregate rollups cannot be un-counted. This action cannot be undone.</AlertDialogDescription>
              </AlertDialogHeader>
              <AlertDialogFooter>
                <AlertDialogCancel>Cancel</AlertDialogCancel>
                <AlertDialogAction disabled={remove.isPending} variant="destructive" onClick={() => remove.mutate(profileId)}>Delete profile permanently</AlertDialogAction>
              </AlertDialogFooter>
            </AlertDialogContent>
          </AlertDialog>
        ) : null}
      </div>
      <Card>
        <CardHeader>
          <CardTitle>Traits</CardTitle>
          <CardDescription>Latest properties supplied by identify.</CardDescription>
        </CardHeader>
        <CardContent>
          {Object.keys(profile.data.traits).length === 0 ? (
            <p className="text-sm text-muted-foreground">No traits recorded.</p>
          ) : (
            <dl className="grid gap-3 sm:grid-cols-2">
              {Object.entries(profile.data.traits).map(([key, value]) => (
                <div className="flex flex-col gap-1" key={key}>
                  <dt className="text-sm text-muted-foreground">{key}</dt>
                  <dd className="break-words text-sm font-medium">{displayValue(value)}</dd>
                </div>
              ))}
            </dl>
          )}
        </CardContent>
      </Card>
      <section className="flex flex-col gap-4">
        <header className="flex flex-col gap-1">
          <h2 className="text-lg font-semibold">Recent events</h2>
          <p className="text-sm text-muted-foreground">Events still available in the raw-data hot tier.</p>
        </header>
        {profile.data.timeline.length === 0 ? (
          <Empty><EmptyHeader><EmptyTitle>No retained events</EmptyTitle><EmptyDescription>This profile has no events in the hot tier.</EmptyDescription></EmptyHeader></Empty>
        ) : (
          <div className="rounded-xl border bg-card">
            <Table aria-label="Profile event timeline">
              <TableHeader><TableRow><TableHead>Time</TableHead><TableHead>Event</TableHead><TableHead>Path</TableHead>{canReplay ? <TableHead>Replay</TableHead> : null}</TableRow></TableHeader>
              <TableBody>
                {profile.data.timeline.map((event, index) => (
                  <TableRow key={`${event.ts}-${event.name}-${index}`}>
                    <TableCell>{new Date(event.ts).toLocaleString()}</TableCell>
                    <TableCell className="font-medium">{event.name || event.kind}</TableCell>
                    <TableCell>{event.path || '—'}</TableCell>
                    {canReplay ? <TableCell>{org && site ? <ReplayLink hasReplay={event.has_replay} org={org} site={site} sessionId={event.session_id} onOpen={onReplayChange ? (id) => onReplayChange(id) : undefined} /> : null}</TableCell> : null}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <ListPager
          fetching={profile.isFetching}
          hasNext={Boolean(data.next_cursor)}
          page={page}
          pageSize={50}
          rowCount={data.timeline.length}
          total={data.total}
          totalCapped={data.total_capped}
          onFirst={onFirst}
          onNext={() => data.next_cursor && onNext(data.next_cursor)}
          onPrevious={onPrevious}
        />
      </section>
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
    </div>
  )
}

export function ProfileDetailPage(props: ComponentProps<typeof ProfileDetailPageContent>) {
  const allowed = useCapability('profiles.read')
  if (!allowed) return <AccessDenied subject="read profiles" />
  return <ProfileDetailPageContent {...props} />
}
