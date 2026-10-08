import { ReplayLink } from './ReplayLink'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { useReplays } from './api'

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`
  return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

export function ReplaysPage({
  siteId,
  org,
  site,
  from,
  to,
  cursor,
  onCursorChange,
}: {
  siteId: number
  org: string
  site: string
  from: string
  to: string
  cursor?: string
  onCursorChange: (cursor?: string) => void
}) {
  const replays = useReplays(siteId, from, to, cursor)
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
            <TableHeader><TableRow><TableHead>Play</TableHead><TableHead>Started</TableHead><TableHead>Duration</TableHead><TableHead>Pages</TableHead><TableHead>Trigger</TableHead><TableHead>Size</TableHead></TableRow></TableHeader>
            <TableBody>{replays.data.items.map((replay) => (
              <TableRow key={replay.session_id}>
                <TableCell><ReplayLink hasReplay org={org} site={site} sessionId={replay.session_id} /></TableCell>
                <TableCell><time dateTime={replay.started_at}>{new Date(replay.started_at).toLocaleString()}</time></TableCell>
                <TableCell>{formatDuration(replay.duration_s)}</TableCell>
                <TableCell>{replay.pages}</TableCell>
                <TableCell>{replay.trigger}</TableCell>
                <TableCell>{formatBytes(replay.bytes)}</TableCell>
              </TableRow>
            ))}</TableBody>
          </Table>
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button disabled={!cursor || replays.isFetching} variant="outline" onClick={() => onCursorChange(undefined)}>First page</Button>
        <Button disabled={!replays.data?.next_cursor || replays.isFetching} variant="outline" onClick={() => onCursorChange(replays.data?.next_cursor ?? undefined)}>Next page</Button>
      </div>
    </main>
  )
}
