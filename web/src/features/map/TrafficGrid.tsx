import { useId, useMemo, useRef, useState } from 'react'

import { ChartTooltip, rectFromElement, type TooltipRect } from '@/components/ChartTooltip'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { rampStep } from '@/features/map/rampStep'
import { fmt } from '@/lib/format'
import { inspectTargetFor, sameInspect, type InspectControl } from '@/lib/inspect'
import { cn } from '@/lib/utils'
import { en } from '@/messages/en'
import { ui } from '@/messages/ui'

import {
  hourLabel,
  marginTotals,
  orderedWeekdays,
  peakTrafficCell,
  peakTrafficWindows,
  quietestWeekday,
  trafficCells,
  weekpartShares,
  type TrafficCell,
  type TrafficMetric,
} from './trafficGrid'

type TrafficGridProps = {
  dateRange?: { from: string; to: string }
  rows: Array<Record<string, unknown>>
  metric: TrafficMetric
  timezone: string
  locale?: string
  isLoading?: boolean
  error?: { detail?: string } | null
  onRetry?(): void
  onShorten?(days: number): void
  inspect?: InspectControl
}

const stepClasses = ['bg-muted/35', 'bg-accent/20', 'bg-accent/40', 'bg-accent/60', 'bg-accent/80', 'bg-accent'] as const

function timezoneLabel(timezone: string, locale?: string): string {
  try {
    const offset = new Intl.DateTimeFormat(locale, { timeZone: timezone, timeZoneName: 'shortOffset' })
      .formatToParts(new Date())
      .find((part) => part.type === 'timeZoneName')?.value
    return offset ? `${timezone}, ${offset}` : timezone
  } catch {
    return timezone
  }
}

function rangeDateLabel(value: string, locale?: string): string {
  return new Intl.DateTimeFormat(locale, {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    timeZone: 'UTC',
  }).format(new Date(`${value}T12:00:00Z`))
}

function retentionDays(detail?: string): number | undefined {
  const match = detail?.match(/site's (\d+)-day raw retention window/i)
  return match ? Number(match[1]) : undefined
}

function cellLabel(cell: TrafficCell, weekday: string, metric: string, locale?: string): string {
  return `${weekday} ${hourLabel(cell.hour, locale)}: ${fmt.number(cell.value)} ${metric}`
}

export function TrafficGrid({ dateRange, rows, metric, timezone, locale, isLoading = false, error, onRetry, onShorten, inspect }: TrafficGridProps) {
  const summaryId = useId()
  const inspectHintId = useId()
  const weekdays = useMemo(() => orderedWeekdays(locale), [locale])
  const cells = useMemo(() => trafficCells(rows, metric, locale), [rows, metric, locale])
  const totals = useMemo(() => marginTotals(cells), [cells])
  const maximum = cells.reduce((largest, cell) => Math.max(largest, cell.value), 0)
  const peak = peakTrafficCell(cells)
  const peaks = peakTrafficWindows(cells)
  const weekparts = weekpartShares(cells)
  const quietest = quietestWeekday(cells)
  const [activeIndex, setActiveIndex] = useState(0)
  const [tooltipIndex, setTooltipIndex] = useState<number>()
  const [tooltipAnchor, setTooltipAnchor] = useState<TooltipRect>()
  const [tooltipAvoid, setTooltipAvoid] = useState<TooltipRect>()
  const [touchedIndex, setTouchedIndex] = useState<number>()
  const cellRefs = useRef<Array<HTMLButtonElement | null>>([])
  const axisRef = useRef<HTMLDivElement>(null)
  const metricLabel = metric === 'visitors' ? en.map.visitors.toLowerCase() : en.map.pageviews.toLowerCase()
  const days = retentionDays(error?.detail)
  const selectedCell = touchedIndex === undefined ? undefined : cells[touchedIndex]
  const quietestName = quietest ? weekdays.find((day) => day.index === quietest.weekday)?.name : undefined
  const total = cells.reduce((sum, cell) => sum + cell.value, 0)
  const low = cells.reduce<TrafficCell | undefined>((lowest, cell) => !lowest || cell.value < lowest.value ? cell : lowest, undefined)
  const rangeLabel = dateRange
    ? { from: rangeDateLabel(dateRange.from, locale), to: rangeDateLabel(dateRange.to, locale) }
    : { from: en.a11y2.selectedRange, to: '' }
  const peakDay = peak ? weekdays.find((day) => day.index === peak.weekday)?.name ?? '' : ''
  const lowDay = low ? weekdays.find((day) => day.index === low.weekday)?.name ?? '' : ''
  const trafficValue = (cell: TrafficCell | undefined, weekday: string) => cell
    ? en.a11y2.weeklyValue.replace('{value}', fmt.number(cell.value)).replace('{weekday}', weekday).replace('{hour}', hourLabel(cell.hour, locale))
    : '0'
  const dataSummary = dateRange
    ? en.a11y2.dataSummary
      .replace('{title}', 'Weekly rhythm')
      .replace('{from}', rangeLabel.from)
      .replace('{to}', rangeLabel.to)
      .replace('{total}', `${fmt.number(total)} ${metricLabel}`)
      .replace('{peak}', trafficValue(peak, peakDay))
      .replace('{low}', trafficValue(low, lowDay))
    : en.a11y2.selectedDataSummary
      .replace('{title}', 'Weekly rhythm')
      .replace('{range}', rangeLabel.from)
      .replace('{total}', `${fmt.number(total)} ${metricLabel}`)
      .replace('{peak}', trafficValue(peak, peakDay))
      .replace('{low}', trafficValue(low, lowDay))

  const showTooltip = (index: number, element: HTMLElement) => {
    setTooltipIndex(index)
    setTooltipAnchor(rectFromElement(element))
    setTooltipAvoid(axisRef.current ? rectFromElement(axisRef.current) : undefined)
  }

  const hideTooltip = () => {
    setTooltipIndex(undefined)
    setTooltipAnchor(undefined)
    setTooltipAvoid(undefined)
  }

  const moveFocus = (index: number, key: string) => {
    const row = Math.floor(index / 24)
    const hour = index % 24
    let next = index
    if (key === 'ArrowLeft') next = row * 24 + Math.max(0, hour - 1)
    if (key === 'ArrowRight') next = row * 24 + Math.min(23, hour + 1)
    if (key === 'ArrowUp') next = Math.max(0, row - 1) * 24 + hour
    if (key === 'ArrowDown') next = Math.min(6, row + 1) * 24 + hour
    if (key === 'Home') next = row * 24
    if (key === 'End') next = row * 24 + 23
    setActiveIndex(next)
    cellRefs.current[next]?.focus()
  }

  const summary = selectedCell
    ? cellLabel(selectedCell, weekdays[Math.floor(touchedIndex! / 24)].name, metricLabel, locale)
    : peak
      ? en.map.busiest
        .replace('{weekday}', weekdays.find((day) => day.index === peak.weekday)?.name ?? '')
        .replace('{hour}', hourLabel(peak.hour, locale))
        .replace('{value}', fmt.number(peak.value))
        .replace('{metric}', metricLabel)
      : undefined

  return (
    <Card className="flex min-w-0 self-start flex-col" aria-labelledby="weekly-rhythm-title">
      <CardHeader className="gap-1 border-b">
        <CardTitle id="weekly-rhythm-title">Weekly rhythm</CardTitle>
        <CardDescription>{en.map.trafficCaption.replace('{timezone}', timezoneLabel(timezone, locale))}</CardDescription>
        <p className="sr-only" id={summaryId}>{dataSummary}</p>
        {inspect ? <p className="sr-only" id={inspectHintId}>{ui.inspector.cellHint}</p> : null}
        {summary ? <p className="text-sm font-medium" aria-live="polite">{summary}</p> : null}
        {quietestName ? <p className="text-xs text-muted-foreground">Quietest weekday: {quietestName}</p> : null}
      </CardHeader>
      <CardContent className="flex min-h-0 flex-col gap-4 overflow-visible pt-4">
        {isLoading ? (
          <div aria-label={en.map.loadingTraffic} className="grid min-h-56 grid-cols-12 gap-1" role="status">
            {Array.from({ length: 84 }, (_, index) => <Skeleton key={index} className="h-3 w-full" />)}
          </div>
        ) : error ? (
          <div className="flex min-h-56 flex-1 flex-col items-start justify-center gap-3" role="alert">
            <p className="text-sm text-muted-foreground">{days ? en.map.retention.replace('{days}', String(days)) : error.detail || en.map.genericError}</p>
            <Button size="sm" variant="outline" onClick={days ? () => onShorten?.(days) : onRetry}>
              {days ? en.map.useLastDays.replace('{days}', String(days)) : en.map.retry}
            </Button>
          </div>
        ) : (
          <>
            {maximum === 0 ? <div className="text-sm text-muted-foreground" role="status"><p>{en.map.noTraffic}</p><p>{en.polish.noTrafficNext}</p></div> : null}
            <div aria-describedby={summaryId} aria-label="Weekly traffic by weekday and hour" className="relative min-w-0" role="grid">
              <div ref={axisRef} className="grid grid-cols-[2.75rem_repeat(24,minmax(0,1fr))_3rem] gap-px pb-1" role="row">
                <span className="sr-only" role="columnheader">Weekday</span>
                {Array.from({ length: 24 }, (_, hour) => (
                  <span key={hour} className="min-w-0 text-center text-xs leading-4 text-muted-foreground" role="columnheader">
                    <span className="sr-only">{hourLabel(hour, locale)}</span>
                    {hour % 6 === 0
                      ? <span aria-hidden="true">{hourLabel(hour, locale).replace(/\s/g, '')}</span>
                      : hour % 3 === 0
                        ? <span aria-hidden="true" className="hidden xl:inline">{hourLabel(hour, locale).replace(/\s/g, '')}</span>
                        : ''}
                  </span>
                ))}
                <span className="sr-only" role="columnheader">Day total</span>
              </div>
              {weekdays.map((weekday, row) => (
                <div key={weekday.index} className="grid min-h-7 grid-cols-[2.75rem_repeat(24,minmax(0,1fr))_3rem] items-center gap-px" data-weekday-row role="row">
                  <span className="truncate pr-1 text-xs text-muted-foreground" role="rowheader" title={weekday.name}>{weekday.short}</span>
                  {cells.slice(row * 24, row * 24 + 24).map((cell, hour) => {
                    const index = row * 24 + hour
                    const label = cellLabel(cell, weekday.name, metricLabel, locale)
                    const busiest = peak?.weekday === cell.weekday && peak.hour === cell.hour
                    const target = inspectTargetFor('weekday_hour', [String(cell.weekday), String(cell.hour)])
                    const inspectTarget = target && inspect?.can(target) ? target : undefined
                    return (
                      <button
                        key={cell.hour}
                        ref={(node) => { cellRefs.current[index] = node }}
                        aria-label={label}
                        aria-describedby={inspectTarget ? inspectHintId : undefined}
                        aria-selected={inspectTarget ? sameInspect(inspect?.selected, inspectTarget) : undefined}
                        onClick={inspectTarget ? (event) => inspect?.open(inspectTarget, event.currentTarget) : undefined}
                        className={cn(
                          'relative h-6 min-w-0 rounded-[3px] outline-none focus-visible:z-10 focus-visible:ring-2 focus-visible:ring-ring',
                          stepClasses[rampStep(cell.value, maximum)],
                          busiest ? 'ring-2 ring-ring' : null,
                        )}
                        data-busiest={busiest || undefined}
                        role="gridcell"
                        tabIndex={activeIndex === index ? 0 : -1}
                        type="button"
                        onBlur={hideTooltip}
                        onFocus={(event) => { setActiveIndex(index); showTooltip(index, event.currentTarget) }}
                        onKeyDown={(event) => {
                          if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) {
                            event.preventDefault()
                            moveFocus(index, event.key)
                          }
                        }}
                        onMouseEnter={(event) => showTooltip(index, event.currentTarget)}
                        onMouseLeave={hideTooltip}
                        onPointerDown={(event) => { if (event.pointerType === 'touch') setTouchedIndex(index) }}
                      >
                        {busiest ? <span aria-hidden="true" className="absolute right-0 top-0 size-1 rounded-full bg-foreground" /> : null}
                      </button>
                    )
                  })}
                  <span aria-label={`${weekday.name} total: ${fmt.number(totals.weekday[row])} ${metricLabel}`} className="relative h-5 overflow-hidden rounded-sm bg-muted text-right text-xs leading-5 tabular-nums" role="gridcell">
                    <span aria-hidden="true" className="absolute inset-y-0 left-0 bg-accent/50" style={{ width: `${maximum > 0 ? (totals.weekday[row] / Math.max(...totals.weekday)) * 100 : 0}%` }} />
                    <span className="relative px-1">{fmt.number(totals.weekday[row])}</span>
                  </span>
                </div>
              ))}
              <div className="mt-1 grid min-h-10 grid-cols-[2.75rem_repeat(24,minmax(0,1fr))_3rem] gap-px" role="row">
                <span className="sr-only" role="rowheader">Hourly totals</span>
                {totals.hour.map((value, hour) => (
                  <span key={hour} aria-label={`${hourLabel(hour, locale)} total: ${fmt.number(value)} ${metricLabel}`} className="relative min-h-10 rounded-sm bg-muted" role="gridcell">
                    <span aria-hidden="true" className="absolute inset-x-0 bottom-0 rounded-sm bg-accent/50" style={{ height: `${Math.max(...totals.hour) > 0 ? (value / Math.max(...totals.hour)) * 100 : 0}%` }} />
                  </span>
                ))}
                <span aria-label="All hours" role="gridcell" />
              </div>
            </div>
            {tooltipIndex !== undefined ? <ChartTooltip anchor={tooltipAnchor} avoid={tooltipAvoid} className="flex items-center gap-1">
                  <span>{weekdays[Math.floor(tooltipIndex / 24)].name}</span>
                  <span>{hourLabel(cells[tooltipIndex].hour, locale)}</span>
                  <span>{fmt.number(cells[tooltipIndex].value)} {metricLabel}</span>
                </ChartTooltip> : null}
            <div className="flex items-center gap-1 text-xs text-muted-foreground">
              <span>{en.map.fewer}</span>
              <span aria-label="Traffic intensity legend" className="flex gap-1" role="img">{[1, 2, 3, 4, 5].map((step) => <span key={step} aria-hidden="true" className={cn('size-3 rounded-[2px]', stepClasses[step])} />)}</span>
              <span>{en.map.more}</span>
            </div>
            <section className="border-t pt-4" aria-labelledby="peak-windows-title">
              <h3 id="peak-windows-title" className="text-sm font-medium">Peak windows</h3>
              {peaks.length > 0 ? <ol className="mt-3 space-y-3">
                {peaks.map((window) => {
                  const weekday = weekdays.find((day) => day.index === window.weekday)?.name ?? ''
                  return <li key={`${window.weekday}:${window.hour}`} className="text-xs">
                    <span>{weekday} {hourLabel(window.hour, locale)}, {fmt.number(window.value)} {metricLabel}</span>
                    <span aria-hidden="true" className="mt-1 block h-1.5 overflow-hidden rounded-full bg-muted">
                      <span className="block h-full rounded-full bg-accent" style={{ width: `${maximum > 0 ? (window.value / maximum) * 100 : 0}%` }} />
                    </span>
                  </li>
                })}
              </ol> : <p className="mt-2 text-xs text-muted-foreground">No busy windows in this range.</p>}
              <div className="mt-4 grid grid-cols-2 gap-3 text-xs">
                <div><p>Weekdays {weekparts.weekdays}%</p><span aria-hidden="true" className="mt-1.5 block h-1.5 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-accent" style={{ width: `${weekparts.weekdays}%` }} /></span></div>
                <div><p>Weekends {weekparts.weekends}%</p><span aria-hidden="true" className="mt-1.5 block h-1.5 overflow-hidden rounded-full bg-muted"><span className="block h-full rounded-full bg-chart-2" style={{ width: `${weekparts.weekends}%` }} /></span></div>
              </div>
            </section>
            <table className="sr-only" aria-label="Weekly traffic data">
              <thead><tr><th>Weekday</th><th>Hour</th><th>{metric === 'visitors' ? en.map.visitors : en.map.pageviews}</th></tr></thead>
              <tbody>{cells.map((cell, index) => <tr key={`${cell.weekday}:${cell.hour}`}><th>{weekdays[Math.floor(index / 24)].name}</th><td>{hourLabel(cell.hour, locale)}</td><td>{cell.value}</td></tr>)}</tbody>
            </table>
          </>
        )}
      </CardContent>
    </Card>
  )
}
