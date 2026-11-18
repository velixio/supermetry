import type { components } from '@/lib/api.gen'

export type ObsMetricQuery = components['schemas']['ObsMetricQuery']
export type ObsMetricWidgetQuery = components['schemas']['ObsMetricWidgetQuery']
export type ObsMetricInfo = components['schemas']['ObsMetricInfo']
export type ObsMetricSeries = components['schemas']['ObsMetricSeries']
export type ObsMetricResult = components['schemas']['ObsMetricResult']
export type MetricKind = ObsMetricInfo['kind']
export type Agg = ObsMetricQuery['agg']
export type Interval = ObsMetricQuery['interval']

/** Aggregations the server accepts for each metric kind (percentiles need a histogram, rate a counter or histogram). */
export const aggsByKind: Record<MetricKind, readonly Agg[]> = {
  gauge: ['avg', 'min', 'max', 'last', 'sum'],
  updown: ['avg', 'min', 'max', 'last', 'sum'],
  counter: ['sum', 'rate', 'avg', 'min', 'max', 'last'],
  histogram: ['p50', 'p75', 'p90', 'p95', 'p99', 'avg', 'sum', 'rate', 'min', 'max'],
}

export const intervals: readonly Interval[] = ['1m', '5m', '1h', '1d']
export const maxGroupBy = 2
export const maxBuckets = 1500

const minute = 60_000
const hour = 60 * minute
const day = 24 * hour

export const ranges = [
  { key: '1h', ms: hour },
  { key: '6h', ms: 6 * hour },
  { key: '24h', ms: day },
  { key: '7d', ms: 7 * day },
  { key: '30d', ms: 30 * day },
] as const
export type RangeKey = (typeof ranges)[number]['key']

const intervalMs: Record<Interval, number> = { '1m': minute, '5m': 5 * minute, '1h': hour, '1d': day }

/** A readable default step for a range: minutes for hours, hours for weeks. */
export function defaultInterval(rangeMs: number): Interval {
  if (rangeMs <= 6 * hour) return '1m'
  if (rangeMs <= 2 * day) return '5m'
  if (rangeMs <= 31 * day) return '1h'
  return '1d'
}

/** The server answers 1m and 5m from raw points for ranges up to 48 hours only, and allows 1,500 buckets. */
export function fitInterval(interval: Interval, rangeMs: number): Interval {
  let current = interval
  if (rangeMs > 2 * day && intervalMs[current] < hour) current = '1h'
  for (const step of intervals) {
    if (intervalMs[step] < intervalMs[current]) continue
    if (rangeMs / intervalMs[step] <= maxBuckets) return step
  }
  return '1d'
}

export type FilterRow = { key: string; value: string }

export type ExplorerState = {
  name: string
  agg: Agg
  groupBy: string[]
  filters: FilterRow[]
  range: RangeKey
  /** 'auto' picks defaultInterval for the range. */
  interval: Interval | 'auto'
  chart: 'line' | 'bar'
}

export const initialState: ExplorerState = { name: '', agg: 'avg', groupBy: [], filters: [], range: '24h', interval: 'auto', chart: 'line' }

/** The MetricQuery the explorer sends for its state at `now`. Empty filter rows and duplicate group-by keys are dropped. */
export function buildMetricQuery(state: ExplorerState, now: Date): ObsMetricQuery {
  const rangeMs = ranges.find((range) => range.key === state.range)?.ms ?? day
  const wanted = state.interval === 'auto' ? defaultInterval(rangeMs) : state.interval
  const filters: Record<string, string> = {}
  for (const row of state.filters) {
    if (row.key.trim() !== '' && row.value !== '') filters[row.key.trim()] = row.value
  }
  const query: ObsMetricQuery = {
    name: state.name,
    agg: state.agg,
    from: new Date(now.getTime() - rangeMs).toISOString(),
    to: now.toISOString(),
    interval: fitInterval(wanted, rangeMs),
  }
  const groupBy = [...new Set(state.groupBy.filter((key) => key !== ''))].slice(0, maxGroupBy)
  if (groupBy.length > 0) query.group_by = groupBy
  if (Object.keys(filters).length > 0) query.filters = filters
  return query
}

/** A dashboard widget query: the explorer query without its time range, which the dashboard supplies. */
export function toWidgetQuery(query: ObsMetricQuery): ObsMetricWidgetQuery {
  const { from: _from, to: _to, ...rest } = query
  void _from
  void _to
  return rest
}

/** Midnight of a YYYY-MM-DD date in an IANA time zone, as an instant. */
export function zonedDayStart(date: string, timeZone: string): Date {
  const [y, m, d] = date.split('-').map(Number)
  const format = new Intl.DateTimeFormat('en-US', {
    timeZone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric', second: 'numeric',
  })
  const offsetAt = (instant: number) => {
    const parts = Object.fromEntries(format.formatToParts(new Date(instant)).map((part) => [part.type, Number(part.value)]))
    return Date.UTC(parts.year, parts.month - 1, parts.day, parts.hour, parts.minute, parts.second) - instant
  }
  const wall = Date.UTC(y, m - 1, d)
  let instant = wall - offsetAt(wall)
  instant = wall - offsetAt(instant)
  return new Date(instant)
}

/** The instants a dashboard's inclusive local date range covers, for a widget query. */
export function dateRangeInstants(range: { from: string; to: string }, timeZone: string): { from: string; to: string } {
  const start = zonedDayStart(range.from, timeZone)
  const [y, m, d] = range.to.split('-').map(Number)
  const next = new Date(Date.UTC(y, m - 1, d + 1)).toISOString().slice(0, 10)
  return { from: start.toISOString(), to: zonedDayStart(next, timeZone).toISOString() }
}

/** The label of one result series: its group values, or the metric name when ungrouped. */
export function seriesLabel(series: ObsMetricSeries, fallback: string): string {
  const values = Object.entries(series.group)
  return values.length === 0 ? fallback : values.map(([key, value]) => `${key}=${value || '∅'}`).join(', ')
}
