import { PlusIcon, RefreshCwIcon, XIcon } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'

import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { obsMetrics as t } from '@/messages/obsMetrics'

import { useObsMetricQuery, useObsMetrics } from './api'
import { MetricChart } from './MetricChart'
import {
  aggsByKind, buildMetricQuery, initialState, intervals, maxGroupBy, ranges,
  type Agg, type ExplorerState, type Interval, type ObsMetricInfo, type RangeKey,
} from './metricQuery'

const none = '__none'
const auto = 'auto'

type Props = {
  siteId: number
  timezone: string
  initial?: Partial<ExplorerState>
}

function Pick({ id, label, value, onChange, options, placeholder }: {
  id: string
  label: string
  value: string
  onChange: (value: string) => void
  options: Array<{ value: string; label: string }>
  placeholder?: string
}) {
  return (
    <Field>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Select value={value} onValueChange={onChange}>
        <SelectTrigger className="min-h-11 w-full sm:min-h-9" id={id}><SelectValue placeholder={placeholder} /></SelectTrigger>
        <SelectContent>
          <SelectGroup>
            {options.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}
          </SelectGroup>
        </SelectContent>
      </Select>
    </Field>
  )
}

const floorMinute = (date: Date) => new Date(Math.floor(date.getTime() / 60_000) * 60_000)

/** The metric explorer: pick a metric, an aggregation, up to two group-by keys and filters, and chart the result. */
export function MetricExplorer({ siteId, timezone, initial }: Props) {
  const metrics = useObsMetrics(siteId)
  const [state, setState] = useState<ExplorerState>({ ...initialState, ...initial })
  const [now, setNow] = useState(() => floorMinute(new Date()))
  useEffect(() => {
    const timer = window.setInterval(() => setNow(floorMinute(new Date())), 60_000)
    return () => window.clearInterval(timer)
  }, [])

  const info: ObsMetricInfo | undefined = metrics.data?.find((metric) => metric.name === state.name)
  const aggs = info ? aggsByKind[info.kind] : []
  const query = useMemo(() => (info ? buildMetricQuery(state, now) : null), [info, state, now])
  const result = useObsMetricQuery(siteId, query)

  const patch = (change: Partial<ExplorerState>) => setState((current) => ({ ...current, ...change }))
  const chooseMetric = (name: string) => {
    const next = metrics.data?.find((metric) => metric.name === name)
    if (!next) return
    const allowed = aggsByKind[next.kind]
    patch({ name, groupBy: [], filters: [], agg: allowed.includes(state.agg) ? state.agg : allowed[0] })
  }
  const setGroup = (index: number, key: string) => {
    const next = [...state.groupBy]
    if (key === none) next.splice(index)
    else next[index] = key
    patch({ groupBy: next })
  }
  const setFilter = (index: number, change: Partial<{ key: string; value: string }>) =>
    patch({ filters: state.filters.map((row, i) => (i === index ? { ...row, ...change } : row)) })
  const keys = info?.attribute_keys ?? []

  if (metrics.isPending) return <Skeleton aria-busy="true" aria-label={t.loading} className="h-64 w-full" role="status" />
  if (metrics.isError) return <ErrorState onRetry={() => void metrics.refetch()} />
  if (metrics.data.length === 0) return <p className="text-sm text-muted-foreground">{t.noMetrics}</p>

  return (
    <div className="flex flex-col gap-4">
      <form className="flex flex-col gap-4 rounded-xl border bg-card p-4" onSubmit={(event) => event.preventDefault()}>
        <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <Pick id="obs-metric" label={t.metric} placeholder={t.metricPlaceholder} value={state.name} onChange={chooseMetric}
            options={metrics.data.map((metric) => ({ value: metric.name, label: metric.name }))} />
          <Pick id="obs-agg" label={t.aggregation} value={aggs.includes(state.agg) ? state.agg : ''} onChange={(value) => patch({ agg: value as Agg })}
            options={aggs.map((agg) => ({ value: agg, label: t.aggs[agg] }))} />
          <Pick id="obs-range" label={t.range} value={state.range} onChange={(value) => patch({ range: value as RangeKey })}
            options={ranges.map((range) => ({ value: range.key, label: t.ranges[range.key] }))} />
          <Pick id="obs-interval" label={t.interval} value={state.interval} onChange={(value) => patch({ interval: value === auto ? auto : (value as Interval) })}
            options={[{ value: auto, label: t.intervalAuto }, ...intervals.map((step) => ({ value: step, label: t.intervals[step] }))]} />
        </FieldGroup>

        {info ? (
          <FieldGroup className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            {Array.from({ length: Math.min(maxGroupBy, state.groupBy.length + 1) }, (_, index) => (
              <Pick id={`obs-group-${index}`} key={index} label={index === 0 ? t.groupBy : t.groupByThen}
                value={state.groupBy[index] ?? none} onChange={(value) => setGroup(index, value)}
                options={[{ value: none, label: t.none }, ...keys.filter((key) => !state.groupBy.includes(key) || state.groupBy[index] === key).map((key) => ({ value: key, label: key }))]} />
            ))}
          </FieldGroup>
        ) : null}

        {info ? (
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">{t.filters}</legend>
            {state.filters.map((row, index) => (
              <div className="flex items-end gap-2" key={index}>
                <Pick id={`obs-filter-key-${index}`} label={t.filterKey} value={row.key} onChange={(value) => setFilter(index, { key: value })}
                  options={keys.map((key) => ({ value: key, label: key }))} />
                <Field>
                  <FieldLabel htmlFor={`obs-filter-value-${index}`}>{t.filterValue}</FieldLabel>
                  <Input className="min-h-11 sm:min-h-9" id={`obs-filter-value-${index}`} maxLength={256} value={row.value} onChange={(event) => setFilter(index, { value: event.target.value })} />
                </Field>
                <Button aria-label={t.removeFilter} className="min-h-11 min-w-11 sm:min-h-9 sm:min-w-9" size="icon" type="button" variant="outline"
                  onClick={() => patch({ filters: state.filters.filter((_, i) => i !== index) })}><XIcon /></Button>
              </div>
            ))}
            {state.filters.length < 16 && keys.length > 0 ? (
              <Button className="min-h-11 w-fit sm:min-h-9" size="sm" type="button" variant="outline"
                onClick={() => patch({ filters: [...state.filters, { key: keys[0], value: '' }] })}><PlusIcon />{t.addFilter}</Button>
            ) : null}
          </fieldset>
        ) : null}

        <div className="flex flex-wrap items-center justify-between gap-2">
          <ToggleGroup aria-label={t.chartType} type="single" value={state.chart}
            onValueChange={(value) => { if (value === 'line' || value === 'bar') patch({ chart: value }) }}>
            <ToggleGroupItem className="min-h-11 sm:min-h-9" value="line">{t.line}</ToggleGroupItem>
            <ToggleGroupItem className="min-h-11 sm:min-h-9" value="bar">{t.bar}</ToggleGroupItem>
          </ToggleGroup>
          <Button className="min-h-11 sm:min-h-9" size="sm" type="button" variant="outline" onClick={() => { setNow(floorMinute(new Date())); void result.refetch() }}>
            <RefreshCwIcon />{t.refresh}
          </Button>
        </div>
      </form>

      <section aria-label={state.name || t.title} className="rounded-xl border bg-card p-4">
        {!info ? <p className="text-sm text-muted-foreground">{t.pickMetric}</p>
          : result.isError ? <ErrorState onRetry={() => void result.refetch()} />
          : !result.data ? <Skeleton aria-busy="true" aria-label={t.loading} className="h-64 w-full" role="status" />
          : result.data.series.every((series) => series.points.length === 0) ? <p className="text-sm text-muted-foreground">{t.noData}</p>
          : <MetricChart chart={state.chart} interval={query?.interval ?? '1h'} name={state.name} series={result.data.series} timezone={timezone} />}
      </section>
    </div>
  )
}
