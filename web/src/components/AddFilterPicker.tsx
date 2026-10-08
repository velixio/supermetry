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
import type { Filter, FilterOperator } from '@/lib/search'

const dimensions = [
  'page', 'source', 'country', 'region', 'city', 'browser', 'os', 'device',
  'utm_source', 'utm_medium', 'utm_campaign', 'utm_content', 'utm_term', 'event', 'property',
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
  const [operator, setOperator] = useState<FilterOperator>('is')
  const [value, setValue] = useState('')
  const actualDimension = dimension === 'property'
    ? propertyKey.trim() === '' ? '' : `prop:${propertyKey.trim()}`
    : dimension
  const suggestionDimension = useDebounced(actualDimension, suggestionDelayMs)
  const suggestionQuery = useDebounced(value, suggestionDelayMs)
  const suggestions = useSuggestions(siteId, orgId, dateRange, suggestionDimension, suggestionQuery, open && suggestionDimension !== '')

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const trimmed = value.trim()
    if (trimmed === '' || actualDimension === '') return
    onAdd({ dimension: actualDimension, operator, values: [trimmed] })
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
                  setValue('')
                }}
              >
                {dimensions.map((item) => <option key={item} value={item}>{item}</option>)}
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
              {suggestions.data && suggestions.data.length > 0 ? (
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
            <Button disabled={value.trim() === '' || actualDimension === ''} type="submit">Apply filter</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
