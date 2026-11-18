import { Link } from '@tanstack/react-router'
import { Bar, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'

import { AppEmptyState } from './AppEmptyState'
import { AppFilters } from './AppFilters'
import { useAppIssues, useAppOverview } from './api'
import { formatRate } from './format'
import { IssueRowsTable } from './IssuesTable'
import { ReleaseHealth } from './ReleaseHealth'
import { hasAppFilters, type AppSearch } from './search'

export const SESSIONS_HINT = 'Send a session event at app start to measure this'

function Tile({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl tabular-nums">{children}</CardTitle>
      </CardHeader>
    </Card>
  )
}

export function AppOverview({ org, site, siteId, timezone, search, onSearch }: {
  org: string; site: string; siteId: number; timezone: string; search: AppSearch; onSearch: (next: AppSearch) => void
}) {
  const overview = useAppOverview(siteId, search, timezone)
  const top = useAppIssues(siteId, search, timezone, { status: 'open', limit: 10 })
  if (overview.isPending) return <PageContainer><PageHeader title="Overview" /><p role="status" className="text-sm text-muted-foreground">Loading overview…</p></PageContainer>
  if (overview.isError) return <PageContainer><PageHeader title="Overview" /><p role="alert" className="text-sm text-destructive">{overview.error.message}</p></PageContainer>
  const data = overview.data
  if (data.events === 0 && !hasAppFilters(search)) return <AppEmptyState org={org} site={site} />
  const sessions = data.crash_free_sessions
  return (
    <PageContainer>
      <PageHeader title="Overview" description="Crash-free rates and issues for this app." actions={<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} />} />
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 xl:grid-cols-6" aria-label="Key figures">
        <Tile label="Crash-free users">{formatRate(data.crash_free_users)}</Tile>
        <Tile label="Crash-free sessions">
          {sessions === null ? <span title={SESSIONS_HINT}>n/a</span> : formatRate(sessions)}
        </Tile>
        <Tile label="Events">{fmt.number(data.events)}</Tile>
        <Tile label="Crashes">{fmt.number(data.crashes)}</Tile>
        <Tile label="New issues">{fmt.number(data.new_issues)}</Tile>
        <Tile label="Regressed issues">{fmt.number(data.regressed_issues)}</Tile>
      </div>
      {data.sessions > 0 || search.release ? <ReleaseHealth data={data} /> : null}
      <Card>
        <CardHeader><CardTitle>Events and crashes</CardTitle><CardDescription>Daily counts, with the crash-free users rate.</CardDescription></CardHeader>
        <CardContent>
          {data.series.length === 0 ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No events in this period.</p> : (
            <div className="h-64" role="img" aria-label="Daily events, crashes and crash-free users">
              <ResponsiveContainer width="100%" height="100%">
                <ComposedChart accessibilityLayer data={data.series}>
                  <CartesianGrid vertical={false} strokeDasharray="3 3" />
                  <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
                  <YAxis yAxisId="count" allowDecimals={false} tickLine={false} axisLine={false} width={40} />
                  <YAxis yAxisId="rate" orientation="right" domain={[0, 1]} tickFormatter={(value: number) => `${Math.round(value * 100)}%`} tickLine={false} axisLine={false} width={44} />
                  <Tooltip />
                  <Bar yAxisId="count" dataKey="events" name="Events" fill="var(--chart-2)" />
                  <Bar yAxisId="count" dataKey="crashes" name="Crashes" fill="var(--chart-1)" />
                  <Line yAxisId="rate" dataKey="crash_free_users" name="Crash-free users" stroke="var(--chart-3)" dot={false} connectNulls />
                </ComposedChart>
              </ResponsiveContainer>
            </div>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader className="flex-row items-start justify-between gap-4">
          <div><CardTitle>Top issues</CardTitle><CardDescription>Open issues, most recently seen first.</CardDescription></div>
          <Link className="text-sm font-medium underline" to="/app/$org/$site/issues" params={{ org, site }} search={{ ...search }}>All issues</Link>
        </CardHeader>
        <CardContent>
          {top.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading issues…</p> : null}
          {top.isError ? <p role="alert" className="text-sm text-destructive">{top.error.message}</p> : null}
          {top.data ? <IssueRowsTable org={org} site={site} issues={top.data.issues} search={search} /> : null}
        </CardContent>
      </Card>
    </PageContainer>
  )
}
