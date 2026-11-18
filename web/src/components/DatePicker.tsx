import { useMemo, useState } from 'react'
import { CalendarDaysIcon, ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { cn } from '@/lib/utils'
import { en } from '@/messages/en'
import { dashboardView } from '@/messages/dashboardView'
import { compareValues, periodValues, type Compare, type DashboardSearch, type Period } from '../lib/search'
import { activeLocale, weekStart } from '@/lib/i18n/intl'

export type DatePickerLabels = {
  period: string
  periods: Record<Period, string>
  from: string
  to: string
  compare: string
  comparisons: Record<Compare, string>
}

type DatePickerProps = {
  search: DashboardSearch
  /** The resolved range. With it the date button reads "Last 30 days · Oct 20 – Nov 18". */
  dateRange?: { from: string; to: string }
  labels: DatePickerLabels
  onChange: (search: DashboardSearch) => void
}

function dateValue(value?: string): Date | undefined {
  return value ? new Date(`${value}T12:00:00Z`) : undefined
}

function isoDay(value: Date): string {
  return value.toISOString().slice(0, 10)
}

/** Both dates, with the year only when the range does not stay inside one year. */
export function formatRange(from: string, to: string): string {
  const sameYear = from.slice(0, 4) === to.slice(0, 4)
  const format = new Intl.DateTimeFormat(activeLocale(), sameYear ? { month: 'short', day: 'numeric', timeZone: 'UTC' } : { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
  return `${format.format(dateValue(from))} – ${format.format(dateValue(to))}`
}

export function rangeLabel(search: DashboardSearch, labels: DatePickerLabels, dateRange?: { from: string; to: string }): string {
  if (search.period !== 'custom') {
    return dateRange ? dashboardView.dateButton(labels.periods[search.period], formatRange(dateRange.from, dateRange.to)) : labels.periods[search.period]
  }
  if (!search.from) return labels.periods.custom
  const format = new Intl.DateTimeFormat(activeLocale(), { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
  const from = format.format(dateValue(search.from))
  const to = search.to ? format.format(dateValue(search.to)) : en.polish.selectEnd
  return `${from} – ${to}`
}

/** Short weekday names in the locale's own week order, starting on its first day. */
function weekdayNames(): string[] {
  const names = new Intl.DateTimeFormat(activeLocale(), { weekday: 'short', timeZone: 'UTC' })
  return Array.from({ length: 7 }, (_, index) => names.format(new Date(Date.UTC(2024, 0, 7 + ((weekStart() + index) % 7)))))
}

function monthDays(month: Date): Date[] {
  const first = new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth(), 1))
  const start = new Date(first)
  start.setUTCDate(1 - ((first.getUTCDay() - weekStart() + 7) % 7))
  return Array.from({ length: 42 }, (_, index) => {
    const day = new Date(start)
    day.setUTCDate(start.getUTCDate() + index)
    return day
  })
}

export function DatePicker({ search, dateRange, labels, onChange }: DatePickerProps) {
  const initial = dateValue(search.from) ?? new Date()
  const [month, setMonth] = useState(() => new Date(Date.UTC(initial.getUTCFullYear(), initial.getUTCMonth(), 1)))
  const [draftFrom, setDraftFrom] = useState<string>()
  const [selectingEnd, setSelectingEnd] = useState(false)
  const days = useMemo(() => monthDays(month), [month])
  const selectedFrom = draftFrom ?? search.from
  const selectedTo = selectingEnd ? undefined : search.to
  const monthName = new Intl.DateTimeFormat(activeLocale(), { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(month)
  const fullDay = new Intl.DateTimeFormat(activeLocale(), { month: 'long', day: 'numeric', year: 'numeric', timeZone: 'UTC' })

  const selectDay = (day: Date) => {
    const value = isoDay(day)
    if (!selectingEnd || !selectedFrom || value < selectedFrom) {
      setDraftFrom(value)
      setSelectingEnd(true)
      onChange({ ...search, period: 'custom', from: value, to: undefined })
      return
    }
    setSelectingEnd(false)
    onChange({ ...search, period: 'custom', from: selectedFrom, to: value })
  }

  return (
    <div className="-m-1 flex max-w-full min-w-0 items-center gap-2 overflow-x-auto p-1 [&>*]:shrink-0">
      <Popover>
        <PopoverTrigger asChild>
          <Button aria-label={`${en.polish.chooseRange}: ${rangeLabel(search, labels, dateRange)}`} className="max-sm:min-h-11 justify-start" type="button" variant="outline">
            <CalendarDaysIcon data-icon="inline-start" />
            {rangeLabel(search, labels, dateRange)}
          </Button>
        </PopoverTrigger>
        <PopoverContent aria-label={en.polish.chooseRange} className="w-[min(22rem,calc(100vw-2rem))]" align="end">
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1">
              <h2 className="font-medium">{labels.period}</h2>
              <p className="text-sm text-muted-foreground">{en.polish.chooseRangeDescription}</p>
            </div>
            <Select value={search.period} onValueChange={(value) => onChange({ ...search, period: value as Period })}>
              <SelectTrigger aria-label={en.polish.preset} className="max-sm:min-h-11 w-full"><SelectValue /></SelectTrigger>
              <SelectContent><SelectGroup>{periodValues.map((period) => <SelectItem key={period} value={period}>{labels.periods[period]}</SelectItem>)}</SelectGroup></SelectContent>
            </Select>
            <div>
              <div className="mb-2 flex items-center justify-between">
                <Button aria-label={en.polish.previousMonth} className="max-sm:size-11" size="icon" type="button" variant="ghost" onClick={() => setMonth(new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth() - 1, 1)))}><ChevronLeftIcon /></Button>
                <p aria-live="polite" className="text-sm font-medium">{monthName}</p>
                <Button aria-label={en.polish.nextMonth} className="max-sm:size-11" size="icon" type="button" variant="ghost" onClick={() => setMonth(new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth() + 1, 1)))}><ChevronRightIcon /></Button>
              </div>
              <div aria-label={monthName} className="grid grid-cols-7" role="grid">
                {weekdayNames().map((weekday) => <span key={weekday} className="py-1 text-center text-xs text-muted-foreground" role="columnheader">{weekday}</span>)}
                {days.map((day) => {
                  const value = isoDay(day)
                  const outside = day.getUTCMonth() !== month.getUTCMonth()
                  const inRange = Boolean(selectedFrom && selectedTo && value >= selectedFrom && value <= selectedTo)
                  const endpoint = value === selectedFrom || value === selectedTo
                  return <Button
                    key={value}
                    aria-label={`Choose ${fullDay.format(day)}`}
                    aria-pressed={endpoint}
                    className={cn('max-sm:size-11 p-0 tabular-nums', outside ? 'text-muted-foreground opacity-50' : null, inRange ? 'bg-muted' : null)}
                    size="icon"
                    type="button"
                    variant={endpoint ? 'default' : 'ghost'}
                    onClick={() => selectDay(day)}
                  >{day.getUTCDate()}</Button>
                })}
              </div>
            </div>
          </div>
        </PopoverContent>
      </Popover>
      <Select value={search.compare} onValueChange={(value) => onChange({ ...search, compare: value as Compare })}>
        <SelectTrigger aria-label={labels.compare} className="max-sm:min-h-11 min-w-44" data-testid="compare-control">
          <SelectValue>{`${labels.compare}: ${labels.comparisons[search.compare]}`}</SelectValue>
        </SelectTrigger>
        <SelectContent><SelectGroup>{compareValues.map((compare) => <SelectItem key={compare} value={compare}>{labels.comparisons[compare]}</SelectItem>)}</SelectGroup></SelectContent>
      </Select>
    </div>
  )
}
