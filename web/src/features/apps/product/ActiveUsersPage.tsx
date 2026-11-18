import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { fmt } from '@/lib/format'

import { AppFilters } from '../AppFilters'
import { formatDay } from '../format'
import type { AppSearch } from '../search'
import { AdoptionCard } from './AdoptionCard'
import { formatStickiness, useActiveUsers } from './api'
import { EngagementCard } from './EngagementCard'
import { FeatureSets } from './FeatureSets'

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

export function ActiveUsersPage({ siteId, timezone, search, onSearch, canEdit }: {
  siteId: number; timezone: string; search: AppSearch; onSearch: (next: AppSearch) => void; canEdit: boolean
}) {
  const active = useActiveUsers(siteId, search, timezone)
  const header = <PageHeader title="Sessions & Users" description="Daily, weekly and monthly active installs, engagement and feature adoption." actions={<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} />} />
  if (active.isPending) return <PageContainer>{header}<p role="status" className="text-sm text-muted-foreground">Loading users…</p></PageContainer>
  if (active.isError) return <PageContainer>{header}<p role="alert" className="text-sm text-destructive">{active.error.message}</p></PageContainer>
  const data = active.data
  const last = data.series.at(-1)
  const chart = data.series.map((point) => ({ ...point, day: formatDay(point.day) }))
  const newInstalls = data.new_installs.reduce((sum, item) => sum + item.count, 0)
  return (
    <PageContainer>
      {header}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-5" aria-label="Active installs">
        <Tile label="DAU (latest day)">{fmt.number(last?.dau ?? 0)}</Tile>
        <Tile label="WAU">{fmt.number(last?.wau ?? 0)}</Tile>
        <Tile label="MAU">{fmt.number(last?.mau ?? 0)}</Tile>
        <Tile label="Stickiness">{formatStickiness(data.stickiness)}</Tile>
        <Tile label="New installs">{fmt.number(newInstalls)}</Tile>
      </div>
      <Card>
        <CardHeader><CardTitle>Active installs</CardTitle><CardDescription>Distinct installs per day, over the last 7 days and over the last 30 days.</CardDescription></CardHeader>
        <CardContent>
          {chart.length === 0 ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No activity in this period.</p> : (
            <div className="h-64" role="img" aria-label="Daily, weekly and monthly active installs">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart accessibilityLayer data={chart}>
                  <CartesianGrid vertical={false} strokeDasharray="3 3" />
                  <XAxis dataKey="day" tickLine={false} axisLine={false} minTickGap={24} />
                  <YAxis allowDecimals={false} tickLine={false} axisLine={false} width={40} />
                  <Tooltip />
                  <Line dataKey="dau" name="DAU" stroke="var(--chart-1)" dot={false} />
                  <Line dataKey="wau" name="WAU" stroke="var(--chart-2)" dot={false} />
                  <Line dataKey="mau" name="MAU" stroke="var(--chart-3)" dot={false} />
                </LineChart>
              </ResponsiveContainer>
            </div>
          )}
        </CardContent>
      </Card>
      <EngagementCard siteId={siteId} search={search} timezone={timezone} />
      <AdoptionCard siteId={siteId} search={search} timezone={timezone} />
      <FeatureSets siteId={siteId} search={search} timezone={timezone} canEdit={canEdit} />
    </PageContainer>
  )
}
