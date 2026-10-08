import { Link } from '@tanstack/react-router'
import { SearchIcon, UserRoundIcon } from 'lucide-react'
import { useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

import { useProfiles } from './api'

function formatDate(value: string): string {
  if (!value) return '—'
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' }).format(new Date(value))
}

export function ProfilesPage({
  siteId,
  orgId,
  orgSlug,
  siteSlug,
}: {
  siteId: number
  orgId: number
  orgSlug: string
  siteSlug: string
}) {
  const [search, setSearch] = useState('')
  const [cursors, setCursors] = useState<string[]>([])
  const cursor = cursors.at(-1)
  const profiles = useProfiles(siteId, orgId, search, cursor)

  function changeSearch(value: string) {
    setSearch(value)
    setCursors([])
  }

  return (
    <main className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-xl font-semibold tracking-tight">Profiles</h1>
        <p className="text-sm text-muted-foreground">Understand identified users across sessions and events.</p>
      </header>
      {profiles.isError ? (
        <ErrorState detail={profiles.error?.message} onRetry={() => void profiles.refetch?.()} />
      ) : profiles.isPending ? (
        <div aria-label="Loading profiles" className="flex flex-col gap-3">
          <Skeleton className="h-9 w-full" />
          <Skeleton className="h-52 w-full" />
        </div>
      ) : !profiles.data?.profiles_enabled && !search && !cursor && profiles.data?.data.length === 0 ? (
        <Empty>
          <UserRoundIcon aria-hidden="true" />
          <EmptyHeader>
            <EmptyTitle>Profiles are disabled</EmptyTitle>
            <EmptyDescription>Enable profiles in General site settings, then identify users from your site.</EmptyDescription>
          </EmptyHeader>
          <code className="max-w-full overflow-x-auto rounded-md bg-muted px-3 py-2 text-sm">panel.identify(&quot;user-123&quot;, {'{ plan: "pro" }'});</code>
          <p className="text-sm text-muted-foreground">Call identify on every page load. Panel stores nothing in the browser.</p>
        </Empty>
      ) : (
        <section className="flex flex-col gap-4">
          <div className="relative max-w-md">
            <SearchIcon aria-hidden="true" className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              aria-label="Search profiles"
              className="pl-9"
              placeholder="Search external ID"
              role="searchbox"
              value={search}
              onChange={(event) => changeSearch(event.target.value)}
            />
          </div>
          {profiles.data.data.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyTitle>No profiles found</EmptyTitle>
                <EmptyDescription>{search ? 'Try a different external ID.' : 'Profiles appear after your first identify call.'}</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="rounded-xl border bg-card">
              <Table aria-label="Profiles">
                <TableHeader>
                  <TableRow>
                    <TableHead>External ID</TableHead>
                    <TableHead>Last seen</TableHead>
                    <TableHead className="text-right">Sessions</TableHead>
                    <TableHead className="text-right">Events</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {profiles.data.data.map((profile) => (
                    <TableRow key={profile.id}>
                      <TableCell className="font-medium">
                        <Link className="hover:underline" params={{ org: orgSlug, site: siteSlug, id: String(profile.id) }} to="/$org/$site/profiles/$id">
                          {profile.external_id}
                        </Link>
                      </TableCell>
                      <TableCell>{formatDate(profile.last_seen_at)}</TableCell>
                      <TableCell className="text-right tabular-nums">{profile.sessions}</TableCell>
                      <TableCell className="text-right tabular-nums">{profile.events}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
          <div className="flex justify-end gap-2">
            <Button disabled={cursors.length === 0 || profiles.isFetching} variant="outline" onClick={() => setCursors((value) => value.slice(0, -1))}>Previous page</Button>
            <Button disabled={!profiles.data.next_cursor || profiles.isFetching} variant="outline" onClick={() => profiles.data?.next_cursor && setCursors((value) => [...value, profiles.data.next_cursor!])}>Next page</Button>
          </div>
        </section>
      )}
    </main>
  )
}
