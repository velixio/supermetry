import { useMemo, useRef, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { dotScale, peakTrafficCell, trafficCells, type TrafficMetric, weekdays } from './trafficGrid'

type TrafficGridProps = {
  rows: Array<Record<string, unknown>>
  metric: TrafficMetric
  timezone: string
  locale?: string
  isLoading?: boolean
  error?: { detail?: string } | null
  onRetry?(): void
  onShorten?(days: number): void
}

function hourLabel(hour: number, locale?: string): string {
  return new Intl.DateTimeFormat(locale, {
    hour: 'numeric',
    timeZone: 'UTC',
  }).format(new Date(Date.UTC(2020, 0, 1, hour)))
}

function timezoneLabel(timezone: string, locale?: string): string {
  try {
    const offset = new Intl.DateTimeFormat(locale, {
      timeZone: timezone,
      timeZoneName: 'shortOffset',
    })
      .formatToParts(new Date())
      .find((part) => part.type === 'timeZoneName')?.value
    return offset ? `${timezone}, ${offset}` : timezone
  } catch {
    return timezone
  }
}

function retentionDays(detail?: string): number | undefined {
  const match = detail?.match(/site's (\d+)-day raw retention window/i)
  return match ? Number(match[1]) : undefined
}

export function TrafficGrid({
  rows,
  metric,
  timezone,
  locale,
  isLoading = false,
  error,
  onRetry,
  onShorten,
}: TrafficGridProps) {
  const cells = useMemo(() => trafficCells(rows, metric), [rows, metric])
  const maximum = cells.reduce((largest, cell) => Math.max(largest, cell.value), 0)
  const peak = peakTrafficCell(cells)
  const [activeIndex, setActiveIndex] = useState(0)
  const [tooltipIndex, setTooltipIndex] = useState<number>()
  const cellRefs = useRef<Array<HTMLButtonElement | null>>([])
  const metricLabel = metric === 'visitors' ? en.map.visitors.toLowerCase() : en.map.pageviews.toLowerCase()
  const days = retentionDays(error?.detail)

  const moveFocus = (index: number, key: string) => {
    const hour = Math.floor(index / 7)
    const weekday = index % 7
    let next = index
    if (key === 'ArrowLeft') next = hour * 7 + ((weekday + 6) % 7)
    if (key === 'ArrowRight') next = hour * 7 + ((weekday + 1) % 7)
    if (key === 'ArrowUp') next = ((hour + 23) % 24) * 7 + weekday
    if (key === 'ArrowDown') next = ((hour + 1) % 24) * 7 + weekday
    if (next === index) return
    setActiveIndex(next)
    setTooltipIndex(next)
    cellRefs.current[next]?.focus()
  }

  return (
    <Card className="flex h-full min-w-0 flex-col">
      <CardHeader className="gap-1 border-b">
        <CardTitle>{en.map.trafficTitle}</CardTitle>
        <CardDescription>
          {en.map.trafficCaption.replace('{timezone}', timezoneLabel(timezone, locale))}
        </CardDescription>
        {peak ? (
          <p className="text-sm font-medium">
            {en.map.busiest
              .replace('{weekday}', weekdays[peak.weekday])
              .replace('{hour}', hourLabel(peak.hour, locale))
              .replace('{value}', fmt.number(peak.value))
              .replace('{metric}', metricLabel)}
          </p>
        ) : null}
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden pt-4">
        {isLoading ? (
          <div aria-label={en.map.loadingTraffic} className="grid flex-1 grid-cols-7 gap-2">
            {Array.from({ length: 35 }, (_, index) => (
              <Skeleton key={index} className="h-5 w-full" />
            ))}
          </div>
        ) : error ? (
          <div className="flex flex-1 flex-col items-start justify-center gap-3" role="alert">
            <p className="text-sm text-muted-foreground">
              {days ? en.map.retention.replace('{days}', String(days)) : error.detail || en.map.genericError}
            </p>
            <Button size="sm" variant="outline" onClick={days ? () => onShorten?.(Math.min(30, days)) : onRetry}>
              {days ? en.map.useLastDays.replace('{days}', String(Math.min(30, days))) : en.map.retry}
            </Button>
          </div>
        ) : (
          <>
            {!peak ? (
              <p className="text-sm text-muted-foreground" role="status">
                {en.map.noTraffic}
              </p>
            ) : null}
            <div aria-label={en.map.trafficTitle} className="relative min-w-0 overflow-hidden" role="grid">
              <div className="grid grid-cols-[2.75rem_repeat(7,minmax(0,1fr))] items-center" role="row">
                <span aria-hidden="true" />
                {weekdays.map((weekday) => (
                  <span
                    key={weekday}
                    className="truncate text-center text-[10px] text-muted-foreground"
                    role="columnheader"
                    title={weekday}
                  >
                    {weekday.slice(0, 2)}
                  </span>
                ))}
              </div>
              {Array.from({ length: 24 }, (_, hour) => (
                <div
                  key={hour}
                  className="grid h-5 grid-cols-[2.75rem_repeat(7,minmax(0,1fr))] items-center"
                  role="row"
                >
                  <span className="truncate pr-1 text-right text-[10px] tabular-nums text-muted-foreground">
                    {hourLabel(hour, locale)}
                  </span>
                  {cells.slice(hour * 7, hour * 7 + 7).map((cell) => {
                    const index = hour * 7 + cell.weekday
                    const scale = dotScale(cell.value, maximum)
                    const label = `${weekdays[cell.weekday]} ${hourLabel(hour, locale)}: ${fmt.number(cell.value)} ${metricLabel}`
                    return (
                      <button
                        key={cell.weekday}
                        ref={(node) => {
                          cellRefs.current[index] = node
                        }}
                        aria-label={label}
                        className="grid size-full min-w-0 place-items-center rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                        role="gridcell"
                        tabIndex={activeIndex === index ? 0 : -1}
                        type="button"
                        onBlur={() => setTooltipIndex(undefined)}
                        onFocus={() => {
                          setActiveIndex(index)
                          setTooltipIndex(index)
                        }}
                        onKeyDown={(event) => {
                          if (event.key.startsWith('Arrow')) {
                            event.preventDefault()
                            moveFocus(index, event.key)
                          }
                        }}
                        onMouseEnter={() => setTooltipIndex(index)}
                        onMouseLeave={() => setTooltipIndex(undefined)}
                      >
                        <span
                          aria-hidden="true"
                          className="block rounded-full bg-accent"
                          style={{
                            height: scale.diameter,
                            opacity: scale.opacity,
                            width: scale.diameter,
                          }}
                        />
                      </button>
                    )
                  })}
                </div>
              ))}
              {tooltipIndex !== undefined ? (
                <div
                  className="pointer-events-none absolute left-12 top-7 flex items-center gap-2 rounded-md bg-popover px-2 py-1 text-xs text-popover-foreground shadow-md ring-1 ring-border"
                  role="tooltip"
                >
                  <span>{weekdays[cells[tooltipIndex].weekday]}</span>
                  <span>{hourLabel(cells[tooltipIndex].hour, locale)}</span>
                  <span className="tabular-nums text-muted-foreground">
                    {fmt.number(cells[tooltipIndex].value)} {metricLabel}
                  </span>
                </div>
              ) : null}
            </div>
            <div
              className="mt-auto flex items-center gap-2 text-xs text-muted-foreground"
              aria-label={`${en.map.fewer} to ${en.map.more}`}
            >
              <span>{en.map.fewer}</span>
              {[0.2, 0.4, 0.6, 0.8, 1].map((opacity) => (
                <span key={opacity} aria-hidden="true" className="size-2 rounded-full bg-accent" style={{ opacity }} />
              ))}
              <span>{en.map.more}</span>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
