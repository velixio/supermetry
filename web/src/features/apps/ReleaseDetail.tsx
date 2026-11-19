import { Link } from '@tanstack/react-router'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'

import { AppFilters } from './AppFilters'
import { useAppRelease, type AppReleaseDetailData } from './api'
import { DeployList, deployMarkers, deployReferenceLines } from './DeployMarkers'
import { formatRate } from './format'
import { EVENTS_NOTE } from './ReleaseHealth'
import type { AppSearch } from './search'

export function ReleaseIssueList({ org, site, title, description, issues, empty }: {
  org: string; site: string; title: string; description: string; issues: AppReleaseDetailData['new_issues']; empty: string
}) {
  return (
    <Card>
      <CardHeader><CardTitle>{title}</CardTitle><CardDescription>{description}</CardDescription></CardHeader>
      <CardContent>
        {issues.length === 0 ? <p className="text-sm text-muted-foreground">{empty}</p> : (
          <ul className="flex flex-col gap-1 text-sm" aria-label={title}>
            {issues.map((issue) => (
              <li key={issue.id} className="flex items-baseline justify-between gap-3">
                <Link className="min-w-0 truncate underline" to="/app/$org/$site/issues/$issue" params={{ org, site, issue: String(issue.id) }}>{issue.title}</Link>
                <span className="tabular-nums text-muted-foreground">{fmt.number(issue.events)}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

export function RolloutChart({ data, timezone }: { data: AppReleaseDetailData; timezone: string }) {
  const markers = deployMarkers(data.deploys, timezone, data.rollout.map((point) => point.day))
  if (data.rollout.length === 0) return <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No sessions for this release in this period.</p>
  return (
    <>
      <div className="h-64" role="img" aria-label="Adoption and crash-free sessions per day">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart accessibilityLayer data={data.rollout}>
            <CartesianGrid vertical={false} strokeDasharray="3 3" />
            <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
            <YAxis domain={[0, 1]} tickFormatter={(value: number) => `${Math.round(value * 100)}%`} tickLine={false} axisLine={false} width={44} />
            <Tooltip formatter={(value) => formatRate(typeof value === 'number' ? value : null)} />
            <Line dataKey="adoption" name="Adoption" stroke="var(--chart-2)" dot={false} connectNulls />
            <Line dataKey="crash_free_sessions" name="Crash-free sessions" stroke="var(--chart-3)" dot={false} connectNulls />
            {deployReferenceLines(markers)}
          </LineChart>
        </ResponsiveContainer>
      </div>
      <DeployList deploys={data.deploys} timezone={timezone} />
    </>
  )
}

function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader><CardDescription>{label}</CardDescription><CardTitle className="text-2xl tabular-nums">{children}</CardTitle></CardHeader>
    </Card>
  )
}

export function ReleaseDetail({ org, site, siteId, timezone, search, release, onSearch }: {
  org: string; site: string; siteId: number; timezone: string; search: AppSearch; release: string; onSearch: (next: AppSearch) => void
}) {
  const detail = useAppRelease(siteId, release, search, timezone)
  const back = <Link className="text-sm underline" to="/app/$org/$site/releases" params={{ org, site }} search={{ period: search.period }}>All releases</Link>
  if (detail.isPending) return <PageContainer><PageHeader title={release} /><p role="status" className="text-sm text-muted-foreground">Loading release…</p></PageContainer>
  if (detail.isError) return <PageContainer><PageHeader title={release} actions={back} /><p role="alert" className="text-sm text-destructive">{detail.error.message}</p></PageContainer>
  const data = detail.data
  const fromEvents = data.health_source === 'events'
  const note = fromEvents ? <span className="block text-xs font-normal text-muted-foreground">{EVENTS_NOTE}</span> : null
  const health = data.health
  return (
    <PageContainer>
      <PageHeader title={release} description={<span>{data.created ? `Created ${data.created.slice(0, 10)}` : 'Seen in events only'}</span>} actions={<>{back}<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} /></>} />
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge variant={data.status === 'archived' ? 'secondary' : 'outline'}>{data.status}</Badge>
        {data.ref ? <span className="text-muted-foreground">ref <span className="font-mono">{data.ref}</span></span> : null}
        <span className="text-muted-foreground">{data.commit_count === 0 ? 'No commits' : `${fmt.number(data.commit_count)} ${data.commit_count === 1 ? 'commit' : 'commits'}`}</span>
      </div>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4" aria-label="Release figures">
        <Stat label="Adoption">{formatRate(health.adoption)}</Stat>
        <Stat label="Crash-free sessions">{formatRate(health.crash_free_sessions)}{note}</Stat>
        <Stat label="Crash-free users">{formatRate(health.crash_free_users)}{note}</Stat>
        <Stat label="Sessions">{fmt.number(health.sessions)}</Stat>
      </div>
      <Card>
        <CardHeader><CardTitle>Rollout</CardTitle><CardDescription>Share of the app&apos;s sessions on this release and its crash-free rate{fromEvents ? ` (${EVENTS_NOTE})` : ''}. Dashed lines are deploys.</CardDescription></CardHeader>
        <CardContent><RolloutChart data={data} timezone={timezone} /></CardContent>
      </Card>
      <div className="grid gap-4 lg:grid-cols-2">
        <ReleaseIssueList org={org} site={site} title="New issues" description="First seen in this release, by event count." issues={data.new_issues} empty="No new issues." />
        <ReleaseIssueList org={org} site={site} title="Regressed issues" description="Issues that came back in this release." issues={data.regressed_issues} empty="No regressions." />
      </div>
    </PageContainer>
  )
}
