import { Link } from '@tanstack/react-router'
import { useState } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { fmt } from '@/lib/format'

import { AppFilters } from './AppFilters'
import { useAppReleases } from './api'
import { formatDay, formatRate } from './format'
import { EVENTS_NOTE } from './ReleaseHealth'
import type { AppSearch } from './search'

export function ReleasesTable({ org, site, siteId, timezone, search, onSearch }: {
  org: string; site: string; siteId: number; timezone: string; search: AppSearch; onSearch: (next: AppSearch) => void
}) {
  const releases = useAppReleases(siteId, search, timezone)
  const [picked, setPicked] = useState<string[]>([])
  const toggle = (release: string, on: boolean) => setPicked((current) => on ? (current.includes(release) ? current : [...current, release].slice(-2)) : current.filter((item) => item !== release))
  const issuesLink = (release: string, status: 'all' | 'regressed', count: number) => count === 0
    ? <span className="text-muted-foreground">0</span>
    : <Link className="underline" to="/app/$org/$site/issues" params={{ org, site }} search={{ period: search.period, release, status }}>{fmt.number(count)}</Link>
  const note = (source: string) => source === 'events' ? <span className="block text-xs font-normal text-muted-foreground">{EVENTS_NOTE}</span> : null
  return (
    <PageContainer>
      <PageHeader title="Releases" description="Newest first, with crash-free rates and issue counts." actions={<>
        {picked.length === 2
          ? <Button asChild variant="outline" size="sm"><Link to="/app/$org/$site/releases/compare" params={{ org, site }} search={{ period: search.period, a: picked[0], b: picked[1] }}>Compare selected</Link></Button>
          : <Button variant="outline" size="sm" disabled title="Tick two releases to compare them">Compare selected</Button>}
        <AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} />
      </>} />
      {releases.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading releases…</p> : null}
      {releases.isError ? <p role="alert" className="text-sm text-destructive">{releases.error.message}</p> : null}
      {releases.data && releases.data.releases.length === 0 ? <p className="rounded-lg bg-muted/30 p-8 text-center text-sm text-muted-foreground">No releases reported in this period.</p> : null}
      {releases.data && releases.data.releases.length > 0 ? (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-8"><span className="sr-only">Compare</span></TableHead><TableHead>Release</TableHead><TableHead>First seen</TableHead><TableHead>Last seen</TableHead>
              <TableHead className="text-right">Adoption</TableHead><TableHead className="text-right">Crash-free sessions</TableHead><TableHead className="text-right">Crash-free users</TableHead><TableHead className="text-right">Sessions</TableHead><TableHead className="text-right">Installs</TableHead>
              <TableHead className="text-right">New issues</TableHead><TableHead className="text-right">Regressed</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {releases.data.releases.map((item) => (
              <TableRow key={item.release}>
                <TableCell><Checkbox aria-label={`Compare ${item.release}`} checked={picked.includes(item.release)} onCheckedChange={(on) => toggle(item.release, on === true)} /></TableCell>
                <TableCell className="font-medium"><Link className="underline" to="/app/$org/$site/releases/$release" params={{ org, site, release: item.release }} search={{ period: search.period }}>{item.release}</Link></TableCell>
                <TableCell>{formatDay(item.first_seen)}</TableCell>
                <TableCell>{formatDay(item.last_seen)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatRate(item.adoption)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatRate(item.crash_free_sessions)}{note(item.health_source)}</TableCell>
                <TableCell className="text-right tabular-nums">{formatRate(item.crash_free_users)}{note(item.health_source)}</TableCell>
                <TableCell className="text-right tabular-nums">{fmt.number(item.sessions)}</TableCell>
                <TableCell className="text-right tabular-nums">{fmt.number(item.installs)}</TableCell>
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
