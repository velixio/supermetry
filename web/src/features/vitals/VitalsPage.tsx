import { useState } from 'react'
import { GaugeIcon } from 'lucide-react'
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

import { Flag } from '@/components/Flag'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { BrowserLabel } from '@/components/visitor/BrowserLabel'
import { countryName } from '@/lib/country'
import { cn } from '@/lib/utils'
import type { Filter } from '@/lib/search'

import { useVitals, type VitalMetric, type VitalMetricName, type VitalsDimension, type VitalsResponse } from './api'
import { activeLocale } from '@/lib/i18n/intl'

type VitalsPageProps = {
  siteId: number
  dateRange: { from: string; to: string }
  filters: Filter[]
}

const metrics: Array<{ key: VitalMetricName; name: string; short: string }> = [
  { key: 'lcp', name: 'Largest Contentful Paint', short: 'LCP' },
  { key: 'cls', name: 'Cumulative Layout Shift', short: 'CLS' },
  { key: 'inp', name: 'Interaction to Next Paint', short: 'INP' },
  { key: 'fcp', name: 'First Contentful Paint', short: 'FCP' },
  { key: 'ttfb', name: 'Time to First Byte', short: 'TTFB' },
]

const breakdownDimensions: Array<{ value: VitalsDimension; label: string }> = [
  { value: 'page', label: 'Page' },
  { value: 'device', label: 'Device' },
  { value: 'country', label: 'Country' },
  { value: 'browser', label: 'Browser' },
]

function thresholds(metric: VitalMetricName): [number, number] {
  if (metric === 'lcp') return [2500, 4000]
  if (metric === 'cls') return [100, 250]
  if (metric === 'inp') return [200, 500]
  if (metric === 'fcp') return [1800, 3000]
  return [800, 1800]
}

function rating(metric: VitalMetricName, value?: number): 'Good' | 'Needs improvement' | 'Poor' | 'No data' {
  if (value === undefined) return 'No data'
  const [good, poor] = thresholds(metric)
  if (value <= good) return 'Good'
  return value <= poor ? 'Needs improvement' : 'Poor'
}

function formatP75(metric: VitalMetricName, value?: number): string {
  if (value === undefined) return '—'
  if (metric === 'cls') return (value / 1000).toFixed(3)
  if (value >= 1000) return `${(value / 1000).toFixed(1)} s`
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

function LoadingState() {
  return <div className="flex flex-col gap-4" aria-label="Loading Web Vitals"><div className="grid gap-4 md:grid-cols-3">{metrics.map(({ key }) => <Skeleton className="h-40 rounded-xl" key={key} />)}</div><Skeleton className="h-80 rounded-xl" /></div>
}

function BreakdownValue({ dimension, value }: { dimension: VitalsDimension; value: string }) {
  if (dimension === 'country') {
    return <span className="flex items-center gap-2"><Flag code={value} /><span>{countryName(value)}</span></span>
  }
  if (dimension === 'browser') return <BrowserLabel value={value} />
  return value
}

function formatDay(value: string): string {
  return new Intl.DateTimeFormat(activeLocale(), { month: 'short', day: 'numeric', timeZone: 'UTC' }).format(new Date(`${value}T00:00:00Z`))
}

function TrendChart({ timeseries }: { timeseries: VitalsResponse['timeseries'] }) {
  const [metric, setMetric] = useState<VitalMetricName>('lcp')
  const definition = metrics.find((item) => item.key === metric) ?? metrics[0]
  const [good, poor] = thresholds(metric)
  const points = timeseries.map((point) => ({ date: point.date, ...point[metric] }))

  return (
    <Card aria-label="Web Vitals trend" role="region">
      <CardHeader className="flex flex-col items-start justify-between gap-3 sm:flex-row sm:items-center">
        <div><CardTitle>Daily trend</CardTitle><CardDescription>Site-timezone p50, p75, and p95 from retained raw samples.</CardDescription></div>
        <Tabs className="w-full sm:w-auto" value={metric} onValueChange={(value) => setMetric(value as VitalMetricName)}>
          <TabsList aria-label="Vitals trend metric" className="grid h-auto w-full grid-cols-3 sm:grid-cols-5 sm:w-auto">
            {metrics.map((item) => <TabsTrigger key={item.key} value={item.key}>{item.short}</TabsTrigger>)}
          </TabsList>
        </Tabs>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="h-64 min-h-64 w-full" aria-label={`${definition.name} daily percentiles`} role="img">
          <ResponsiveContainer width="100%" height="100%" minWidth={0}>
            <LineChart accessibilityLayer data={points} margin={{ left: 4, right: 8 }}>
              <CartesianGrid vertical={false} />
              <XAxis dataKey="date" axisLine={false} tickLine={false} minTickGap={24} tickFormatter={formatDay} />
              <YAxis yAxisId="value" axisLine={false} tickLine={false} width={56} tickFormatter={(value: number) => formatP75(metric, value)} />
              <YAxis yAxisId="samples" hide />
              <Tooltip
                labelFormatter={(value) => formatDay(String(value))}
                formatter={(value, name) => name === 'samples'
                  ? [Number(value).toLocaleString(), 'Samples']
                  : [formatP75(metric, Number(value)), String(name).toUpperCase()]}
              />
              <ReferenceLine yAxisId="value" y={good} stroke="var(--chart-2)" strokeDasharray="4 4" />
              <ReferenceLine yAxisId="value" y={poor} stroke="var(--destructive)" strokeDasharray="4 4" />
              <Line yAxisId="value" dataKey="p50" name="p50" type="monotone" stroke="var(--chart-3)" strokeWidth={1.5} dot={false} connectNulls={false} />
              <Line yAxisId="value" dataKey="p75" name="p75" type="monotone" stroke="var(--chart-1)" strokeWidth={2.5} dot={false} connectNulls={false} />
              <Line yAxisId="value" dataKey="p95" name="p95" type="monotone" stroke="var(--chart-4)" strokeWidth={1.5} dot={false} connectNulls={false} />
              <Line yAxisId="samples" dataKey="samples" name="samples" stroke="transparent" dot={false} activeDot={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
          <span className="flex flex-wrap items-center gap-3"><span><i className="mr-1 inline-block h-0.5 w-3 bg-chart-3 align-middle" />p50</span><span><i className="mr-1 inline-block h-0.5 w-3 bg-chart-1 align-middle" />p75</span><span><i className="mr-1 inline-block h-0.5 w-3 bg-chart-4 align-middle" />p95</span><span><span className="font-medium text-chart-2">Good</span> ≤ {formatP75(metric, good)} · <span className="font-medium text-destructive">Poor</span> &gt; {formatP75(metric, poor)}</span></span>
          <span>{points.reduce((sum, point) => sum + point.samples, 0).toLocaleString()} samples shown</span>
        </div>
        <table className="sr-only" aria-label={`${definition.name} daily trend data`}>
          <thead><tr><th>Date</th><th>p50</th><th>p75</th><th>p95</th><th>Samples</th></tr></thead>
          <tbody>{points.map((point) => <tr key={point.date}><th>{point.date}</th><td>{formatP75(metric, point.p50)}</td><td>{formatP75(metric, point.p75)}</td><td>{formatP75(metric, point.p95)}</td><td>{point.samples}</td></tr>)}</tbody>
        </table>
      </CardContent>
    </Card>
  )
}

export function VitalsPage({ siteId, dateRange, filters }: VitalsPageProps) {
  const [dimension, setDimension] = useState<VitalsDimension>('page')
  const result = useVitals({ siteId, dateRange, filters, dimension })
  const totalSamples = result.data ? metrics.reduce((sum, metric) => sum + result.data.overall[metric.key].samples, 0) : 0
  const dimensionLabel = breakdownDimensions.find((item) => item.value === dimension)?.label ?? dimension

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold">Web Vitals</h1>
        <p className="text-sm text-muted-foreground">Real-user loading, stability, and responsiveness at the 75th percentile.</p>
      </div>
      {result.isPending ? <LoadingState /> : result.isError ? (
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
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-5">
            {metrics.map((metric) => <MetricCard key={metric.key} metric={metric.key} name={metric.name} short={metric.short} value={result.data.overall[metric.key]} />)}
          </div>
          <TrendChart timeseries={result.data.timeseries} />
          <Card aria-label={`Web Vitals by ${dimension}`} role="region">
            <CardHeader className="flex flex-col items-start justify-between gap-3 sm:flex-row sm:items-center">
              <div><CardTitle>Breakdown</CardTitle><CardDescription>p75 and samples by {dimension}</CardDescription></div>
              <Tabs className="w-full sm:w-auto" value={dimension} onValueChange={(value) => setDimension(value as VitalsDimension)}>
                <TabsList aria-label="Vitals breakdown" className="grid h-auto w-full grid-cols-2 sm:inline-flex sm:w-auto">
                  {breakdownDimensions.map((item) => (
                    <TabsTrigger key={item.value} value={item.value}>{item.label}</TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
            </CardHeader>
            <CardContent>
              <Table aria-label={`Web Vitals by ${dimension}`}>
                <TableHeader><TableRow><TableHead>{dimensionLabel}</TableHead>{metrics.map((metric) => <TableHead key={metric.key}>{metric.short} p75 <span className="sr-only">and samples</span></TableHead>)}</TableRow></TableHeader>
                <TableBody>{result.data.breakdown.map((row) => <TableRow key={row.value}><TableCell className="max-w-72 truncate font-medium" title={row.value}><BreakdownValue dimension={dimension} value={row.value} /></TableCell>{metrics.map((metric) => <TableCell key={metric.key}><span className="tabular-nums">{formatP75(metric.key, row[metric.key].p75)}</span><span className="ml-2 text-xs text-muted-foreground">{row[metric.key].samples} samples</span></TableCell>)}</TableRow>)}</TableBody>
              </Table>
            </CardContent>
          </Card>
        </>
      ) : null}
    </div>
  )
}
