import { useState } from 'react'
import { GaugeIcon } from 'lucide-react'

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'
import type { Filter } from '@/lib/search'

import { useVitals, type VitalMetric, type VitalMetricName, type VitalsDimension } from './api'

type VitalsPageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
}

const metrics: Array<{ key: VitalMetricName; name: string; short: string }> = [
  { key: 'lcp', name: 'Largest Contentful Paint', short: 'LCP' },
  { key: 'cls', name: 'Cumulative Layout Shift', short: 'CLS' },
  { key: 'inp', name: 'Interaction to Next Paint', short: 'INP' },
]

function rating(metric: VitalMetricName, value?: number): 'Good' | 'Needs improvement' | 'Poor' | 'No data' {
  if (value === undefined) return 'No data'
  const [good, poor] = metric === 'lcp' ? [2500, 4000] : metric === 'cls' ? [100, 250] : [200, 500]
  if (value <= good) return 'Good'
  return value <= poor ? 'Needs improvement' : 'Poor'
}

function formatP75(metric: VitalMetricName, value?: number): string {
  if (value === undefined) return '—'
  if (metric === 'cls') return (value / 1000).toFixed(3)
  if (metric === 'lcp' && value >= 1000) return `${(value / 1000).toFixed(1)} s`
  return `${Math.round(value)} ms`
}

function Distribution({ value }: { value: VitalMetric }) {
  const good = Math.round(value.good * 100)
  const needs = Math.round(value.needs_improvement * 100)
  const poor = Math.round(value.poor * 100)
  return (
    <div
      aria-label={`${good}% good, ${needs}% needs improvement, ${poor}% poor`}
      className="flex h-2 w-full overflow-hidden rounded-full bg-muted"
      role="img"
    >
      <span className="bg-chart-2" style={{ width: `${good}%` }} />
      <span className="bg-chart-4" style={{ width: `${needs}%` }} />
      <span className="bg-destructive" style={{ width: `${poor}%` }} />
    </div>
  )
}

function MetricCard({ metric, value, name, short }: { metric: VitalMetricName; value: VitalMetric; name: string; short: string }) {
  const state = rating(metric, value.p75)
  return (
    <Card aria-label={name} role="region">
      <CardHeader>
        <CardTitle>{name}</CardTitle>
        <CardDescription>{short} · p75 from {value.samples.toLocaleString()} samples</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-end justify-between gap-3">
          <strong className="text-3xl font-semibold tabular-nums">{formatP75(metric, value.p75)}</strong>
          <span className={cn('text-sm font-medium', state === 'Good' && 'text-chart-2', state === 'Needs improvement' && 'text-chart-4', state === 'Poor' && 'text-destructive', state === 'No data' && 'text-muted-foreground')}>{state}</span>
        </div>
        <Distribution value={value} />
      </CardContent>
    </Card>
  )
}

function LoadingCards() {
  return <div className="grid gap-4 md:grid-cols-3" aria-label="Loading Web Vitals">{metrics.map(({ key }) => <Skeleton className="h-40 rounded-xl" key={key} />)}</div>
}

export function VitalsPage({ siteId, dateRange, filters }: VitalsPageProps) {
  const [dimension, setDimension] = useState<VitalsDimension>('page')
  const result = useVitals({ siteId, dateRange, filters, dimension })
  const totalSamples = result.data ? metrics.reduce((sum, metric) => sum + result.data.overall[metric.key].samples, 0) : 0

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold">Web Vitals</h1>
        <p className="text-sm text-muted-foreground">Real-user loading, stability, and responsiveness at the 75th percentile.</p>
      </div>
      {result.isPending ? <LoadingCards /> : result.isError ? (
        <Empty role="alert"><EmptyHeader><EmptyTitle>Web Vitals could not be loaded</EmptyTitle><EmptyDescription>{result.error.detail ?? 'Try again in a moment.'}</EmptyDescription></EmptyHeader></Empty>
      ) : totalSamples === 0 ? (
        <Empty>
          <GaugeIcon className="size-6 text-muted-foreground" aria-hidden="true" />
          <EmptyHeader>
            <EmptyTitle>No Web Vitals yet</EmptyTitle>
            <EmptyDescription>Web Vitals need the full tracker script. Measurements will appear after visitors load your pages.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : result.data ? (
        <>
          <div className="grid gap-4 md:grid-cols-3">
            {metrics.map((metric) => <MetricCard key={metric.key} metric={metric.key} name={metric.name} short={metric.short} value={result.data.overall[metric.key]} />)}
          </div>
          <Card aria-label={`Web Vitals by ${dimension}`} role="region">
            <CardHeader className="flex items-center justify-between gap-3">
              <div><CardTitle>Breakdown</CardTitle><CardDescription>p75 and samples by {dimension}</CardDescription></div>
              <Tabs value={dimension} onValueChange={(value) => setDimension(value as VitalsDimension)}>
                <TabsList aria-label="Vitals breakdown">
                  <TabsTrigger value="page">Page</TabsTrigger>
                  <TabsTrigger value="device">Device</TabsTrigger>
                </TabsList>
              </Tabs>
            </CardHeader>
            <CardContent>
              <Table aria-label={`Web Vitals by ${dimension}`}>
                <TableHeader><TableRow><TableHead>{dimension === 'page' ? 'Page' : 'Device'}</TableHead>{metrics.map((metric) => <TableHead key={metric.key}>{metric.short} p75 <span className="sr-only">and samples</span></TableHead>)}</TableRow></TableHeader>
                <TableBody>{result.data.breakdown.map((row) => <TableRow key={row.value}><TableCell className="max-w-72 truncate font-medium" title={row.value}>{row.value}</TableCell>{metrics.map((metric) => <TableCell key={metric.key}><span className="tabular-nums">{formatP75(metric.key, row[metric.key].p75)}</span><span className="ml-2 text-xs text-muted-foreground">{row[metric.key].samples} samples</span></TableCell>)}</TableRow>)}</TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      ) : null}
    </div>
  )
}
