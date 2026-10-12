import type { ComponentProps } from 'react'
import { useCapability, AccessDenied } from '@/lib/authorization'
import { Link } from '@tanstack/react-router'
import { SearchIcon, UserRoundIcon } from 'lucide-react'

import { ErrorState } from '@/components/ErrorState'
import { ListPager } from '@/components/ListPager'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'

import { useProfiles } from './api'
import { activeLocale } from '@/lib/i18n/intl'

type Paged<T> = T & Partial<{ total: number; total_capped: boolean }>

function formatDate(value: string): string {
  if (!value) return '—'
  return new Intl.DateTimeFormat(activeLocale(), { dateStyle: 'medium' }).format(new Date(value))
}

function ProfilesPageContent({
  siteId,
  orgId,
  orgSlug,
  siteSlug,
  search,
  cursor,
  page,
  onSearchChange,
  onFirst,
  onPrevious,
  onNext,
}: {
  siteId: number
  orgId: number
  orgSlug: string
  siteSlug: string
  search: string
  cursor?: string
  page: number
  onSearchChange: (search: string) => void
  onFirst: () => void
  onPrevious: () => void
  onNext: (cursor: string) => void
}) {
  const profiles = useProfiles(siteId, orgId, search, cursor)
  const data = profiles.data as Paged<NonNullable<typeof profiles.data>> | undefined

  return (
    <div className="flex min-w-0 flex-col gap-6">
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
          <code className="max-w-full overflow-x-auto rounded-md bg-muted px-3 py-2 text-sm">supermetry.identify(&quot;user-123&quot;, {'{ plan: "pro" }'});</code>
          <p className="text-sm text-muted-foreground">Call identify on every page load. Supermetry stores nothing in the browser.</p>
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
              onChange={(event) => onSearchChange(event.target.value)}
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
                        <Link className="hover:underline" params={{ org: orgSlug, site: siteSlug, id: String(profile.id) }} to="/app/$org/$site/profiles/$id">
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
          <ListPager
            fetching={profiles.isFetching}
            hasNext={Boolean(data?.next_cursor)}
            page={page}
            pageSize={25}
            rowCount={data?.data.length ?? 0}
            total={data?.total}
            totalCapped={data?.total_capped}
            onFirst={onFirst}
            onNext={() => data?.next_cursor && onNext(data.next_cursor)}
            onPrevious={onPrevious}
          />
        </section>
      )}
    </div>
  )
}

export function ProfilesPage(props: ComponentProps<typeof ProfilesPageContent>) {
  const allowed = useCapability('profiles.read')
  if (!allowed) return <AccessDenied subject="read profiles" />
  return <ProfilesPageContent {...props} />
}
