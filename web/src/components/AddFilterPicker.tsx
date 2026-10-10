import { useEffect, useState, type FormEvent } from 'react'
import { PlusIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from '@/components/ui/dialog'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useSuggestions } from '@/features/events/api'
import { CHANNELS } from '@/lib/channels'
import type { Filter, FilterOperator } from '@/lib/search'

const dimensions = [
  { value: 'page', label: 'Page' },
  { value: 'source', label: 'Source' },
  { value: 'channel', label: 'Channel' },
  { value: 'country', label: 'Country' },
  { value: 'region', label: 'Region' },
  { value: 'city', label: 'City' },
  { value: 'browser', label: 'Browser' },
  { value: 'browser_version', label: 'Browser version' },
  { value: 'os', label: 'OS' },
  { value: 'os_version', label: 'OS version' },
  { value: 'device', label: 'Device' },
  { value: 'screen_size', label: 'Screen size' },
  { value: 'language', label: 'Language' },
  { value: 'hostname', label: 'Hostname' },
  { value: 'utm_source', label: 'UTM source' },
  { value: 'utm_medium', label: 'UTM medium' },
  { value: 'utm_campaign', label: 'UTM campaign' },
  { value: 'utm_content', label: 'UTM content' },
  { value: 'utm_term', label: 'UTM term' },
  { value: 'event', label: 'Event' },
  { value: 'property', label: 'Property' },
] as const

const screenSizes = [
  { value: 'mobile', label: 'Mobile' },
  { value: 'tablet', label: 'Tablet' },
  { value: 'laptop', label: 'Laptop' },
  { value: 'desktop', label: 'Desktop' },
  { value: 'large', label: 'Large' },
] as const

const suggestionDelayMs = 250

// useDebounced holds back a fast-changing value so typing sends one
// suggestions request per pause instead of one per keystroke.
function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs)
    return () => clearTimeout(timer)
  }, [value, delayMs])
  return debounced
}

const operators: { label: string; value: FilterOperator }[] = [
  { label: 'is', value: 'is' },
  { label: 'is not', value: 'is_not' },
  { label: 'contains', value: 'contains' },
  { label: 'starts with', value: 'starts_with' },
]

export function AddFilterPicker({
  siteId,
  orgId,
  dateRange,
  onAdd,
}: {
  siteId: number
  orgId: number
  dateRange: { from: string; to: string }
  onAdd: (filter: Filter) => void
}) {
  const [open, setOpen] = useState(false)
  const [dimension, setDimension] = useState<string>('page')
  const [propertyKey, setPropertyKey] = useState('')
  const [countryCode, setCountryCode] = useState('')
  const [operator, setOperator] = useState<FilterOperator>('is')
  const [value, setValue] = useState('')
  const actualDimension = dimension === 'property'
    ? propertyKey.trim() === '' ? '' : `prop:${propertyKey.trim()}`
    : dimension
  const placeNeedsCountry = actualDimension === 'region' || actualDimension === 'city'
  const normalizedCountry = countryCode.trim().toUpperCase()
  const validCountry = /^[A-Z]{2}$/.test(normalizedCountry)
  const suggestionDimension = useDebounced(actualDimension, suggestionDelayMs)
  const suggestionQuery = useDebounced(value, suggestionDelayMs)
  const suggestions = useSuggestions(
    siteId,
    orgId,
    dateRange,
    suggestionDimension,
    suggestionQuery,
    open &&
      suggestionDimension !== '' &&
      suggestionDimension !== 'channel' &&
      suggestionDimension !== 'screen_size' &&
      actualDimension !== 'channel' &&
      actualDimension !== 'screen_size',
  )
  const channelSuggestions = actualDimension === 'channel'
    ? CHANNELS.filter((channel) =>
        channel.toLowerCase().includes(value.trim().toLowerCase()),
      )
    : []
  const screenSizeSuggestions = actualDimension === 'screen_size'
    ? screenSizes.filter((size) =>
        size.label.toLowerCase().includes(value.trim().toLowerCase()),
      )
    : []

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = value.trim()
    if (trimmed === '' || actualDimension === '' || (placeNeedsCountry && !validCountry)) return
    onAdd({
      dimension: actualDimension,
      operator,
      values: placeNeedsCountry ? [trimmed, normalizedCountry] : [trimmed],
    })
    setOpen(false)
    setValue('')
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button type="button" variant="outline">
          <PlusIcon data-icon="inline-start" />
          Add filter
        </Button>
      </DialogTrigger>
      <DialogContent>
        <form className="contents" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>Add filter</DialogTitle>
            <DialogDescription>Choose a dimension, operator, and value.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="filter-dimension">Dimension</FieldLabel>
              <select
                id="filter-dimension"
                className="h-9 rounded-lg border bg-background px-3 text-sm"
                value={dimension}
                onChange={(event) => {
                  setDimension(event.target.value)
                  setPropertyKey('')
                  setCountryCode('')
                  setValue('')
                }}
              >
                {dimensions.map((item) => (
                  <option key={item.value} value={item.value}>{item.label}</option>
                ))}
              </select>
            </Field>
            {dimension === 'property' ? (
              <Field>
                <FieldLabel htmlFor="filter-property-key">Property key</FieldLabel>
                <Input
                  id="filter-property-key"
                  autoComplete="off"
                  value={propertyKey}
                  onChange={(event) => { setPropertyKey(event.target.value); setValue('') }}
                />
              </Field>
            ) : null}
            {placeNeedsCountry ? (
              <Field>
                <FieldLabel htmlFor="filter-country-code">Country code</FieldLabel>
                <Input
                  id="filter-country-code"
                  autoComplete="off"
                  maxLength={2}
                  pattern="[A-Za-z]{2}"
                  placeholder="US"
                  required
                  value={countryCode}
                  onChange={(event) => setCountryCode(event.target.value)}
                />
              </Field>
            ) : null}
            <Field>
              <FieldLabel htmlFor="filter-operator">Operator</FieldLabel>
              <select
                id="filter-operator"
                className="h-9 rounded-lg border bg-background px-3 text-sm"
                value={operator}
                onChange={(event) => setOperator(event.target.value as FilterOperator)}
              >
                {operators.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
              </select>
            </Field>
            <Field>
              <FieldLabel htmlFor="filter-value">Value</FieldLabel>
              <Input id="filter-value" autoComplete="off" value={value} onChange={(event) => setValue(event.target.value)} />
              {actualDimension === 'channel' ? (
                <div className="flex flex-wrap gap-2" role="group" aria-label="Suggested values">
                  {channelSuggestions.map((channel) => (
                    <Button
                      key={channel}
                      aria-label={channel}
                      size="sm"
                      type="button"
                      variant="secondary"
                      onClick={() => setValue(channel)}
                    >
                      {channel}
                    </Button>
                  ))}
                </div>
              ) : actualDimension === 'screen_size' ? (
                <div className="flex flex-wrap gap-2" role="group" aria-label="Suggested values">
                  {screenSizeSuggestions.map((size) => (
                    <Button
                      key={size.value}
                      aria-label={size.label}
                      size="sm"
                      type="button"
                      variant="secondary"
                      onClick={() => setValue(size.value)}
                    >
                      {size.label}
                    </Button>
                  ))}
                </div>
              ) : suggestions.data && suggestions.data.length > 0 ? (
                <div className="flex flex-wrap gap-2" role="group" aria-label="Suggested values">
                  {suggestions.data.map((suggestion) => (
                    <Button
                      key={suggestion.value}
                      aria-label={`${suggestion.value}, ${suggestion.count} events`}
                      size="sm"
                      type="button"
                      variant="secondary"
                      onClick={() => setValue(suggestion.value)}
                    >
                      {suggestion.value}
                    </Button>
                  ))}
                </div>
              ) : null}
            </Field>
          </FieldGroup>
          <DialogFooter>
            <DialogClose asChild><Button type="button" variant="outline">Cancel</Button></DialogClose>
            <Button disabled={value.trim() === '' || actualDimension === '' || (placeNeedsCountry && !validCountry)} type="submit">Apply filter</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
