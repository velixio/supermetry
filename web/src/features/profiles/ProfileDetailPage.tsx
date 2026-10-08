import { useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
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
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import type { Role } from '@/components/Sidebar'

import { useDeleteProfile, useProfile } from './api'

function displayValue(value: unknown): string {
  return typeof value === 'string' ? value : JSON.stringify(value)
}

export function ProfileDetailPage({
  profileId,
  siteId = 0,
  orgId,
  role,
  onDeleted,
}: {
  profileId: number
  siteId?: number
  orgId: number
  role: Role
  onDeleted: () => void
}) {
  const [cursors, setCursors] = useState<string[]>([])
  const cursor = cursors.at(-1)
  const profile = useProfile(profileId, orgId, cursor)
  const remove = useDeleteProfile(siteId || profile.data?.site_id || 0, orgId, onDeleted)
  const canDelete = role === 'owner' || role === 'admin'

  if (profile.isError) return <ErrorState detail={profile.error?.message} onRetry={() => void profile.refetch?.()} />
  if (profile.isPending || !profile.data) return <Skeleton className="h-72 w-full" />

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <header className="flex min-w-0 flex-col gap-1">
          <p className="text-sm text-muted-foreground">Profile</p>
          <h1 className="truncate text-2xl font-semibold tracking-tight">{profile.data.external_id}</h1>
        </header>
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
              <TableHeader><TableRow><TableHead>Time</TableHead><TableHead>Event</TableHead><TableHead>Path</TableHead></TableRow></TableHeader>
              <TableBody>
                {profile.data.timeline.map((event, index) => (
                  <TableRow key={`${event.ts}-${event.name}-${index}`}>
                    <TableCell>{new Date(event.ts).toLocaleString()}</TableCell>
                    <TableCell className="font-medium">{event.name || event.kind}</TableCell>
                    <TableCell>{event.path || '—'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
        <div className="flex justify-end gap-2">
          <Button disabled={cursors.length === 0 || profile.isFetching} variant="outline" onClick={() => setCursors((value) => value.slice(0, -1))}>Previous page</Button>
          <Button disabled={!profile.data.next_cursor || profile.isFetching} variant="outline" onClick={() => profile.data.next_cursor && setCursors((value) => [...value, profile.data.next_cursor!])}>Next page</Button>
        </div>
      </section>
    </main>
  )
}
