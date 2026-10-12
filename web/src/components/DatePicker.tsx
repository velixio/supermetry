import { useMemo, useState } from 'react'
import { CalendarDaysIcon, ChevronLeftIcon, ChevronRightIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { cn } from '@/lib/utils'
import { en } from '@/messages/en'
import { compareValues, periodValues, type Compare, type DashboardSearch, type Period } from '../lib/search'

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
  labels: DatePickerLabels
  onChange: (search: DashboardSearch) => void
}

function dateValue(value?: string): Date | undefined {
  return value ? new Date(`${value}T12:00:00Z`) : undefined
}

function isoDay(value: Date): string {
  return value.toISOString().slice(0, 10)
}

function rangeLabel(search: DashboardSearch, labels: DatePickerLabels): string {
  if (search.period !== 'custom') return labels.periods[search.period]
  if (!search.from) return labels.periods.custom
  const format = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' })
  const from = format.format(dateValue(search.from))
  const to = search.to ? format.format(dateValue(search.to)) : en.polish.selectEnd
  return `${from} – ${to}`
}

function monthDays(month: Date): Date[] {
  const first = new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth(), 1))
  const start = new Date(first)
  start.setUTCDate(1 - first.getUTCDay())
  return Array.from({ length: 42 }, (_, index) => {
    const day = new Date(start)
    day.setUTCDate(start.getUTCDate() + index)
    return day
  })
}

export function DatePicker({ search, labels, onChange }: DatePickerProps) {
  const initial = dateValue(search.from) ?? new Date()
  const [month, setMonth] = useState(() => new Date(Date.UTC(initial.getUTCFullYear(), initial.getUTCMonth(), 1)))
  const [draftFrom, setDraftFrom] = useState<string>()
  const [selectingEnd, setSelectingEnd] = useState(false)
  const days = useMemo(() => monthDays(month), [month])
  const selectedFrom = draftFrom ?? search.from
  const selectedTo = selectingEnd ? undefined : search.to
  const monthName = new Intl.DateTimeFormat('en-US', { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(month)
  const fullDay = new Intl.DateTimeFormat('en-US', { month: 'long', day: 'numeric', year: 'numeric', timeZone: 'UTC' })

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
    <div className="flex w-full flex-col items-stretch gap-3 sm:w-auto sm:flex-row sm:items-center sm:gap-2">
      <Popover>
        <PopoverTrigger asChild>
          <Button aria-label={`${en.polish.chooseRange}: ${rangeLabel(search, labels)}`} className="min-h-11 justify-start" type="button" variant="outline">
            <CalendarDaysIcon data-icon="inline-start" />
            {rangeLabel(search, labels)}
          </Button>
        </PopoverTrigger>
        <PopoverContent aria-label={en.polish.chooseRange} className="w-[min(22rem,calc(100vw-2rem))]" align="end">
          <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-1">
              <h2 className="font-medium">{labels.period}</h2>
              <p className="text-sm text-muted-foreground">{en.polish.chooseRangeDescription}</p>
            </div>
            <Select value={search.period} onValueChange={(value) => onChange({ ...search, period: value as Period })}>
              <SelectTrigger aria-label={en.polish.preset} className="min-h-11 w-full"><SelectValue /></SelectTrigger>
              <SelectContent><SelectGroup>{periodValues.map((period) => <SelectItem key={period} value={period}>{labels.periods[period]}</SelectItem>)}</SelectGroup></SelectContent>
            </Select>
            <div>
              <div className="mb-2 flex items-center justify-between">
                <Button aria-label={en.polish.previousMonth} className="size-11" size="icon" type="button" variant="ghost" onClick={() => setMonth(new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth() - 1, 1)))}><ChevronLeftIcon /></Button>
                <p aria-live="polite" className="text-sm font-medium">{monthName}</p>
                <Button aria-label={en.polish.nextMonth} className="size-11" size="icon" type="button" variant="ghost" onClick={() => setMonth(new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth() + 1, 1)))}><ChevronRightIcon /></Button>
              </div>
              <div aria-label={monthName} className="grid grid-cols-7" role="grid">
                {['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'].map((weekday) => <span key={weekday} className="py-1 text-center text-xs text-muted-foreground" role="columnheader">{weekday}</span>)}
                {days.map((day) => {
                  const value = isoDay(day)
                  const outside = day.getUTCMonth() !== month.getUTCMonth()
                  const inRange = Boolean(selectedFrom && selectedTo && value >= selectedFrom && value <= selectedTo)
                  const endpoint = value === selectedFrom || value === selectedTo
                  return <Button
                    key={value}
                    aria-label={`Choose ${fullDay.format(day)}`}
                    aria-pressed={endpoint}
                    className={cn('size-11 p-0 tabular-nums', outside ? 'text-muted-foreground opacity-50' : null, inRange ? 'bg-muted' : null)}
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
      <label className="flex min-h-11 items-center gap-2 text-sm font-medium">
        <span>{labels.compare}</span>
        <Select value={search.compare} onValueChange={(value) => onChange({ ...search, compare: value as Compare })}>
          <SelectTrigger aria-label={labels.compare} className="min-h-11 min-w-40 flex-1 sm:flex-none"><SelectValue /></SelectTrigger>
          <SelectContent><SelectGroup>{compareValues.map((compare) => <SelectItem key={compare} value={compare}>{labels.comparisons[compare]}</SelectItem>)}</SelectGroup></SelectContent>
        </Select>
      </label>
    </div>
  )
}
