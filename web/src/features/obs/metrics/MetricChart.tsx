import { Bar, BarChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts'

import {
  ChartContainer, ChartLegend, ChartLegendContent, ChartTooltip, ChartTooltipContent, type ChartConfig,
} from '@/components/ui/chart'
import { obsMetrics as t } from '@/messages/obsMetrics'

import { seriesLabel, type ObsMetricSeries } from './metricQuery'

type Props = {
  series: ObsMetricSeries[]
  /** The metric name, used as the label of an ungrouped series. */
  name: string
  chart: 'line' | 'bar'
  timezone: string
  /** Bucket step; days show dates, shorter steps show times. */
  interval: string
  className?: string
}

const palette = ['var(--chart-1)', 'var(--chart-2)', 'var(--chart-3)', 'var(--chart-4)', 'var(--chart-5)']

const compact = new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 2 })
const precise = new Intl.NumberFormat(undefined, { maximumFractionDigits: 4 })

function tickFormatter(timeZone: string, interval: string) {
  const format = new Intl.DateTimeFormat(undefined, interval === '1d'
    ? { timeZone, month: 'short', day: 'numeric' }
    : { timeZone, month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
  return (ts: number) => format.format(new Date(ts))
}

/** Rows keyed by timestamp with one column per series, plus the shadcn chart config naming each column. */
export function chartData(series: ObsMetricSeries[], name: string) {
  const config: ChartConfig = {}
  const rows = new Map<number, Record<string, number | null>>()
  series.forEach((entry, index) => {
    const key = `s${index}`
    config[key] = { label: seriesLabel(entry, name), color: palette[index % palette.length] }
    for (const point of entry.points) {
      const ts = Date.parse(point.ts)
      const row = rows.get(ts) ?? { ts }
      row[key] = point.value
      rows.set(ts, row)
    }
  })
  const data = [...rows.values()].sort((a, b) => (a.ts as number) - (b.ts as number))
  return { config, data, keys: Object.keys(config) }
}

/** Last non-null value per series, shown in a visually hidden table so the chart has a text equivalent. */
function Summary({ series, name }: { series: ObsMetricSeries[]; name: string }) {
  return (
    <table className="sr-only">
      <caption>{t.summary(name)}</caption>
      <tbody>
        {series.map((entry, index) => {
          const last = [...entry.points].reverse().find((point) => point.value !== null)
          return (
            <tr key={index}>
              <th scope="row">{seriesLabel(entry, name)}</th>
              <td>{last ? precise.format(last.value as number) : '—'}</td>
            </tr>
          )
        })}
      </tbody>
    </table>
  )
}

/** A shadcn chart of metric series: lines or bars, one colour per group. */
export function MetricChart({ series, name, chart, timezone, interval, className }: Props) {
  const { config, data, keys } = chartData(series, name)
  const axis = (
    <>
      <CartesianGrid vertical={false} />
      <XAxis axisLine={false} dataKey="ts" domain={['dataMin', 'dataMax']} minTickGap={32} scale="time" tickFormatter={tickFormatter(timezone, interval)} tickLine={false} tickMargin={8} type="number" />
      <YAxis axisLine={false} tickFormatter={(value: number) => compact.format(value)} tickLine={false} width={48} />
      <ChartTooltip content={<ChartTooltipContent labelFormatter={(_, payload) => {
        const ts = payload?.[0]?.payload?.ts
        return typeof ts === 'number' ? new Intl.DateTimeFormat(undefined, { timeZone: timezone, dateStyle: 'medium', timeStyle: interval === '1d' ? undefined : 'short' }).format(new Date(ts)) : ''
      }} />} />
      {keys.length > 1 ? <ChartLegend content={<ChartLegendContent />} /> : null}
    </>
  )
  return (
    <div className="flex flex-col gap-2">
      <ChartContainer className={className ?? 'aspect-auto h-64 w-full'} config={config}>
        {chart === 'bar' ? (
          <BarChart accessibilityLayer data={data}>
            {axis}
            {keys.map((key) => <Bar dataKey={key} fill={`var(--color-${key})`} key={key} radius={2} />)}
          </BarChart>
        ) : (
          <LineChart accessibilityLayer data={data}>
            {axis}
            {keys.map((key) => <Line dataKey={key} dot={false} key={key} stroke={`var(--color-${key})`} strokeWidth={2} type="monotone" />)}
          </LineChart>
        )}
      </ChartContainer>
      <Summary name={name} series={series} />
    </div>
  )
}
