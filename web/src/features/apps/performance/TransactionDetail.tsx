import { Link } from '@tanstack/react-router'
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { fmt } from '@/lib/format'

import { AppFilters } from '../AppFilters'
import { formatMs, formatRate } from '../format'
import type { AppSearch } from '../search'
import { useTransaction, type SpanGroupData } from './api'

export function SpanGroupsTable({ groups, label }: { groups: SpanGroupData[]; label: string }) {
  return (
    <Table aria-label={label}>
      <TableHeader>
        <TableRow>
          <TableHead>Op</TableHead><TableHead>Description</TableHead>
          <TableHead className="text-right">Total time</TableHead><TableHead className="text-right">Avg</TableHead><TableHead className="text-right">p95</TableHead>
          <TableHead className="text-right">Count</TableHead><TableHead className="text-right">Error rate</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {groups.map((group) => (
          <TableRow key={`${group.op}|${group.description}`}>
            <TableCell className="text-muted-foreground">{group.op || 'n/a'}</TableCell>
            <TableCell className="max-w-96 truncate font-mono text-xs" title={group.description}>{group.description || 'n/a'}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMs(group.total_ms)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMs(group.avg_ms)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatMs(group.p95_ms)}</TableCell>
            <TableCell className="text-right tabular-nums">{fmt.number(group.count)}</TableCell>
            <TableCell className="text-right tabular-nums">{formatRate(group.error_rate)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

/** One transaction: p50 and p95 per day, where its time goes (span groups) and the slowest traces. */
export function TransactionDetail({ org, site, siteId, name, timezone, search, onSearch }: {
  org: string; site: string; siteId: number; name: string; timezone: string; search: AppSearch; onSearch: (next: AppSearch) => void
}) {
  const detail = useTransaction(siteId, name, search, timezone)
  const header = <PageHeader title={name} description="Latency, time spent in child spans and the slowest samples." actions={<AppFilters siteId={siteId} timezone={timezone} search={search} onSearch={onSearch} hidePlatform />} />
  const back = <Link className="text-sm font-medium underline" to="/app/$org/$site/performance" params={{ org, site }} search={{ period: search.period }}>Back to performance</Link>
  if (detail.isPending) return <PageContainer>{header}{back}<p role="status" className="text-sm text-muted-foreground">Loading transaction…</p></PageContainer>
  if (detail.isError) return <PageContainer>{header}{back}<p role="alert" className="text-sm text-destructive">{detail.error.message}</p></PageContainer>
  const data = detail.data
  return (
    <PageContainer>
      {header}
      {back}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4" aria-label="Key figures">
        <Figure label="p50">{formatMs(data.summary.p50_ms)}</Figure>
        <Figure label="p95">{formatMs(data.summary.p95_ms)}</Figure>
        <Figure label="Count">{fmt.number(data.summary.count)}</Figure>
        <Figure label="Error rate">{formatRate(data.summary.error_rate)}</Figure>
      </div>
      <Card>
        <CardHeader><CardTitle>Latency</CardTitle><CardDescription>p50 and p95 per day, in milliseconds.</CardDescription></CardHeader>
        <CardContent>
          {data.series.length === 0 ? <p className="rounded-lg border border-dashed p-8 text-center text-sm text-muted-foreground">No samples in this period.</p> : (
            <div className="h-64" role="img" aria-label="p50 and p95 latency per day">
              <ResponsiveContainer width="100%" height="100%">
                <LineChart accessibilityLayer data={data.series}>
                  <CartesianGrid vertical={false} strokeDasharray="3 3" />
                  <XAxis dataKey="date" tickLine={false} axisLine={false} minTickGap={24} />
                  <YAxis tickLine={false} axisLine={false} width={48} unit=" ms" />
                  <Tooltip />
                  <Line dataKey="p50_ms" name="p50" stroke="var(--chart-2)" dot={false} connectNulls />
                  <Line dataKey="p95_ms" name="p95" stroke="var(--chart-1)" dot={false} connectNulls />
                </LineChart>
              </ResponsiveContainer>
            </div>
          )}
        </CardContent>
      </Card>
      {data.app_start.length > 0 ? (
        <Card>
          <CardHeader><CardTitle>App start</CardTitle></CardHeader>
          <CardContent>
            <ul className="flex flex-wrap gap-6 text-sm" aria-label="App start">
              {data.app_start.map((start) => <li key={start.type}><span className="capitalize">{start.type}</span> start: p50 {formatMs(start.p50_ms)}, p95 {formatMs(start.p95_ms)} ({fmt.number(start.count)} starts)</li>)}
            </ul>
          </CardContent>
        </Card>
      ) : null}
      <Card>
        <CardHeader><CardTitle>Span breakdown</CardTitle><CardDescription>Where the time goes, grouped by operation and parameterised description. Raw spans are kept for 14 days at most.</CardDescription></CardHeader>
        <CardContent>
          {data.span_groups.length === 0 ? <p className="text-sm text-muted-foreground">No child spans in this period.</p> : <SpanGroupsTable groups={data.span_groups} label="Span groups" />}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>Slowest samples</CardTitle><CardDescription>Open one to see its waterfall.</CardDescription></CardHeader>
        <CardContent>
          {data.slowest.length === 0 ? <p className="text-sm text-muted-foreground">No samples available.</p> : (
            <Table aria-label="Slowest samples">
              <TableHeader><TableRow><TableHead>Trace</TableHead><TableHead>Started</TableHead><TableHead>Release</TableHead><TableHead className="text-right">Duration</TableHead></TableRow></TableHeader>
              <TableBody>
                {data.slowest.map((sample) => (
                  <TableRow key={`${sample.trace_id}|${sample.start}`}>
                    <TableCell className="font-mono text-xs"><Link className="underline" to="/app/$org/$site/traces/$trace" params={{ org, site, trace: sample.trace_id }}>{sample.trace_id.slice(0, 12)}…</Link></TableCell>
                    <TableCell>{sample.start.slice(0, 19).replace('T', ' ')}</TableCell>
                    <TableCell>{sample.release || 'n/a'}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatMs(sample.duration_ms)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </PageContainer>
  )
}

function Figure({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Card>
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl tabular-nums">{children}</CardTitle>
      </CardHeader>
    </Card>
  )
}
