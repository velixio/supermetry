import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { fmt } from '@/lib/format'

import { AppFilters } from './AppFilters'
import { useAppReleases } from './api'
import { formatDay, formatRate } from './format'
import type { AppSearch } from './search'

export function ReleasesTable({ org, site, siteId, timezone, search, onSearch }: {
  org: string; site: string; siteId: number; timezone: string; search: AppSearch; onSearch: (next: AppSearch) => void
}) {
  const releases = useAppReleases(siteId, search, timezone)
  const issuesLink = (release: string, status: 'all' | 'regressed', count: number) => count === 0
    ? <span className="text-muted-foreground">0</span>
    : <Link className="underline" to="/app/$org/$site/issues" params={{ org, site }} search={{ period: search.period, release, status }}>{fmt.number(count)}</Link>
  return (
    <PageContainer>
      <PageHeader title="Releases" description="Newest first, with crash-free rates and issue counts." actions={<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} />} />
      {releases.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading releases…</p> : null}
      {releases.isError ? <p role="alert" className="text-sm text-destructive">{releases.error.message}</p> : null}
      {releases.data && releases.data.releases.length === 0 ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No releases reported in this period.</p> : null}
      {releases.data && releases.data.releases.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Release</TableHead><TableHead>First seen</TableHead><TableHead>Last seen</TableHead>
              <TableHead className="text-right">Installs</TableHead><TableHead className="text-right">Crash-free users</TableHead><TableHead className="text-right">Crash-free sessions</TableHead>
              <TableHead className="text-right">New issues</TableHead><TableHead className="text-right">Regressed</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {releases.data.releases.map((item) => (
              <TableRow key={item.release}>
                <TableCell className="font-medium">{item.release}</TableCell>
                <TableCell>{formatDay(item.first_seen)}</TableCell>
                <TableCell>{formatDay(item.last_seen)}</TableCell>
                <TableCell className="text-right tabular-nums">{fmt.number(item.installs)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatRate(item.crash_free_users)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatRate(item.crash_free_sessions)}</TableCell>
                <TableCell className="text-right tabular-nums">{issuesLink(item.release, 'all', item.new_issues)}</TableCell>
                <TableCell className="text-right tabular-nums">{issuesLink(item.release, 'regressed', item.regressed_issues)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ) : null}
    </PageContainer>
  )
}
