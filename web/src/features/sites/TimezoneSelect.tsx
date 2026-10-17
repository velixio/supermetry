import { lazy, Suspense, useMemo, useState } from 'react'
import { ChevronsUpDownIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { uiFlows } from '@/messages/uiFlows'

const TimezoneList = lazy(() => import('./TimezoneList'))

const fallbackZones = ['UTC', 'Europe/London', 'Europe/Berlin', 'America/New_York', 'America/Los_Angeles', 'Asia/Kolkata', 'Asia/Kathmandu', 'Asia/Tokyo', 'Australia/Sydney']

/** Every IANA time zone the browser knows, with UTC first. */
export function timeZones(): string[] {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (key: 'timeZone') => string[] }
  const zones = intl.supportedValuesOf?.('timeZone') ?? fallbackZones
  return ['UTC', ...zones.filter((zone) => zone !== 'UTC')]
}

export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}

/**
 * A searchable time-zone picker. It submits its value through a hidden input named `name`, so it
 * works in plain forms, and can also be controlled with value/onChange.
 */
export function TimezoneSelect({
  id = 'site-timezone',
  name = 'timezone',
  value,
  defaultValue,
  onChange,
  error,
  hint,
}: {
  id?: string
  name?: string
  value?: string
  /** Initial zone when uncontrolled; the browser's zone otherwise. */
  defaultValue?: string
  onChange?: (value: string) => void
  error?: string
  hint?: string
}) {
  const [own, setOwn] = useState(() => value ?? defaultValue ?? browserTimeZone())
  const [open, setOpen] = useState(false)
  const current = value ?? own
  const zones = useMemo(() => {
    const all = timeZones()
    return all.includes(current) ? all : [current, ...all]
  }, [current])
  const listId = `${id}-list`
  const hintId = hint ? `${id}-hint` : undefined
  const errorId = error ? `${id}-error` : undefined
  const choose = (zone: string) => {
    setOwn(zone)
    onChange?.(zone)
    setOpen(false)
  }
  return (
    <Field data-invalid={Boolean(error) || undefined}>
      <FieldLabel htmlFor={id}>{uiFlows.site.timezone}</FieldLabel>
      <input type="hidden" name={name} value={current} />
      <Popover open={open} onOpenChange={setOpen}>
        <PopoverTrigger asChild>
          <Button
            id={id}
            type="button"
            variant="outline"
            role="combobox"
            aria-expanded={open}
            aria-controls={listId}
            aria-invalid={Boolean(error) || undefined}
            aria-describedby={[hintId, errorId].filter(Boolean).join(' ') || undefined}
            className="h-9 w-full justify-between bg-background font-normal text-foreground"
          >
            <span className="truncate">{current || uiFlows.site.timezoneChoose}</span>
            <ChevronsUpDownIcon aria-hidden="true" className="opacity-50" />
          </Button>
        </PopoverTrigger>
        <PopoverContent className="w-(--radix-popover-trigger-width) min-w-64 p-0" align="start">
          <Suspense fallback={<p role="status" className="p-3 text-sm text-muted-foreground">{uiFlows.site.timezoneSearch}</p>}>
            <TimezoneList id={listId} zones={zones} current={current} onChoose={choose} />
          </Suspense>
        </PopoverContent>
      </Popover>
      {hint ? <FieldDescription id={hintId}>{hint}</FieldDescription> : null}
      {error ? <FieldError id={errorId}>{error}</FieldError> : null}
    </Field>
  )
}
