import { Link } from '@tanstack/react-router'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'

import { AppFilters } from './AppFilters'
import { useCompareReleases, type AppReleaseDetailData } from './api'
import { formatDeltaPp, formatRate } from './format'
import { ReleaseIssueList } from './ReleaseDetail'
import type { AppSearch } from './search'

function Column({ org, site, search, data }: { org: string; site: string; search: AppSearch; data: AppReleaseDetailData }) {
  const row = (label: string, value: string) => (
    <div className="flex justify-between gap-2 text-sm"><dt className="text-muted-foreground">{label}</dt><dd className="tabular-nums">{value}</dd></div>
  )
  return (
    <Card>
      <CardHeader>
        <CardTitle><Link className="underline" to="/app/$org/$site/releases/$release" params={{ org, site, release: data.release }} search={{ period: search.period }}>{data.release}</Link></CardTitle>
        <CardDescription>{data.health_source === 'events' ? 'Rates from error events.' : 'Rates from tracked sessions.'}</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="flex flex-col gap-1" aria-label={`Release ${data.release}`}>
          {row('Crash-free sessions', formatRate(data.health.crash_free_sessions))}
          {row('Crash-free users', formatRate(data.health.crash_free_users))}
          {row('Sessions', fmt.number(data.health.sessions))}
          {row('Adoption', formatRate(data.health.adoption))}
          {row('New issues', fmt.number(data.new_issues.length))}
          {row('Commits', fmt.number(data.commit_count))}
        </dl>
      </CardContent>
    </Card>
  )
}

export function ReleaseCompare({ org, site, siteId, timezone, search, a, b, onSearch }: {
  org: string; site: string; siteId: number; timezone: string; search: AppSearch; a: string; b: string; onSearch: (next: AppSearch) => void
}) {
  const compare = useCompareReleases(siteId, a, b, search, timezone)
  const back = <Link className="text-sm underline" to="/app/$org/$site/releases" params={{ org, site }} search={{ period: search.period }}>All releases</Link>
  const header = <PageHeader title={`Compare ${a || '?'} and ${b || '?'}`} description="Delta is the second release minus the first." actions={<>{back}<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} /></>} />
  if (a === '' || b === '') return <PageContainer>{header}<p className="text-sm text-muted-foreground">Pick two releases on the releases page to compare them.</p></PageContainer>
  if (compare.isPending) return <PageContainer>{header}<p role="status" className="text-sm text-muted-foreground">Loading comparison…</p></PageContainer>
  if (compare.isError) return <PageContainer>{header}<p role="alert" className="text-sm text-destructive">{compare.error.message}</p></PageContainer>
  const data = compare.data
  const issueDelta = data.delta.new_issues
  return (
    <PageContainer>
      {header}
      <div className="grid gap-4 md:grid-cols-3">
        <Column org={org} site={site} search={search} data={data.a} />
        <Column org={org} site={site} search={search} data={data.b} />
        <Card>
          <CardHeader><CardTitle>Change</CardTitle><CardDescription>{data.b.release} compared with {data.a.release}.</CardDescription></CardHeader>
          <CardContent>
            <dl className="flex flex-col gap-1 text-sm" aria-label="Delta">
              <div className="flex justify-between gap-2"><dt className="text-muted-foreground">Crash-free sessions</dt><dd className="tabular-nums">{formatDeltaPp(data.delta.crash_free_sessions)}</dd></div>
              <div className="flex justify-between gap-2"><dt className="text-muted-foreground">Crash-free users</dt><dd className="tabular-nums">{formatDeltaPp(data.delta.crash_free_users)}</dd></div>
              <div className="flex justify-between gap-2"><dt className="text-muted-foreground">New issues</dt><dd className="tabular-nums">{issueDelta > 0 ? '+' : issueDelta < 0 ? '−' : ''}{fmt.number(Math.abs(issueDelta))}</dd></div>
            </dl>
          </CardContent>
        </Card>
      </div>
      <ReleaseIssueList org={org} site={site} title={`Only in ${data.b.release}`} description={`Issues with events in ${data.b.release} and none in ${data.a.release}.`} issues={data.only_in_b} empty="No issues are unique to this release." />
    </PageContainer>
  )
}
