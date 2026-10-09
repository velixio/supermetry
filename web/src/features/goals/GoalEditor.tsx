import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import type { Goal, GoalInput } from './types'

type GoalEditorProps = {
  goal?: Goal
  pending: boolean
  onSave: (input: GoalInput) => void | Promise<void>
  onCancel?: () => void
  projectCurrency?: string
  currencyState?: 'unconfigured' | 'configured' | 'conflict'
}

const emptyGoal: GoalInput = { kind: 'page', match: 'exact', value: '', name: '' }
export function GoalEditor({ goal, pending, onSave, onCancel, projectCurrency, currencyState }: GoalEditorProps) {
  const [input, setInput] = useState<GoalInput>(goal ?? emptyGoal)
  const [error, setError] = useState('')

  useEffect(() => {
    setInput(goal ?? emptyGoal)
  }, [goal])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!input.name.trim() || !input.value.trim()) {
      setError('Enter a name and value.')
      return
    }
    if (input.kind === 'scroll' && (!Number.isInteger(input.scroll_threshold) || input.scroll_threshold! < 1 || input.scroll_threshold! > 100)) {
      setError('Enter a scroll threshold from 1 to 100.')
      return
    }
    setError('')
    const normalized = { ...input, name: input.name.trim(), value: input.value.trim() }
    if (input.kind === 'event' && input.currency) normalized.currency = input.currency
    else delete normalized.currency
    if (input.kind !== 'scroll') delete normalized.scroll_threshold
    void onSave(normalized)
  }

  return (
    <form className="flex flex-col gap-4 rounded-xl border bg-card p-4" onSubmit={submit}>
      <h2 className="font-medium">{goal ? 'Edit goal' : 'Create a goal'}</h2>
      <FieldGroup>
        <Field data-invalid={error !== '' && !input.name.trim()}>
          <FieldLabel htmlFor="goal-name">Goal name</FieldLabel>
          <Input id="goal-name" aria-invalid={error !== '' && !input.name.trim()} value={input.name} onChange={(event) => setInput((current) => ({ ...current, name: event.target.value }))} />
        </Field>
        <Field>
          <FieldLabel htmlFor="goal-kind">Goal type</FieldLabel>
          <select id="goal-kind" className="h-8 rounded-lg border border-input bg-background px-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50" value={input.kind} onChange={(event) => {
            const kind = event.target.value as GoalInput['kind']
            setInput((current) => {
              if (kind === 'event') return { ...current, kind, scroll_threshold: undefined }
              if (kind === 'scroll') return { ...current, kind, currency: undefined }
              return { ...current, kind, currency: undefined, scroll_threshold: undefined }
            })
          }}>
            <option value="page">Page</option><option value="event">Event</option><option value="scroll">Scroll depth</option>
          </select>
        </Field>
        <Field>
          <FieldLabel htmlFor="goal-match">Match</FieldLabel>
          <select id="goal-match" className="h-8 rounded-lg border border-input bg-background px-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50" value={input.match} onChange={(event) => setInput((current) => ({ ...current, match: event.target.value as GoalInput['match'] }))}>
            <option value="exact">Exact</option><option value="prefix">Prefix</option><option value="wildcard">Wildcard</option>
          </select>
        </Field>
        <Field data-invalid={error !== '' && !input.value.trim()}>
          <FieldLabel htmlFor="goal-value">Goal value</FieldLabel>
          <Input id="goal-value" aria-invalid={error !== '' && !input.value.trim()} value={input.value} onChange={(event) => setInput((current) => ({ ...current, value: event.target.value }))} />
        </Field>
        {input.kind === 'scroll' ? (
          <Field data-invalid={error !== '' && (!Number.isInteger(input.scroll_threshold) || input.scroll_threshold! < 1 || input.scroll_threshold! > 100)}>
            <FieldLabel htmlFor="goal-scroll-threshold">Scroll threshold (%)</FieldLabel>
            <Input
              id="goal-scroll-threshold"
              aria-invalid={error !== '' && (!Number.isInteger(input.scroll_threshold) || input.scroll_threshold! < 1 || input.scroll_threshold! > 100)}
              max={100}
              min={1}
              required
              type="number"
              value={input.scroll_threshold ?? ''}
              onChange={(event) => setInput((current) => ({
                ...current,
                scroll_threshold: event.target.value === '' ? undefined : event.target.valueAsNumber,
              }))}
            />
            <FieldDescription>Counts a visit when someone scrolls at least this far on a matching page</FieldDescription>
          </Field>
        ) : null}
        {input.kind === 'event' ? (
          <Field>
            <label className="flex items-center gap-2 text-sm">
              <input
                aria-label="Track revenue"
                type="checkbox"
                checked={Boolean(input.currency)}
                disabled={currencyState !== 'configured' && !input.currency}
                onChange={(event) => setInput((current) => ({
                  ...current,
                  currency: event.target.checked ? projectCurrency : undefined,
                }))}
              />
              Track revenue{projectCurrency ? ` in ${projectCurrency}` : ''}
            </label>
            <FieldDescription>
              {currencyState === 'conflict'
                ? 'Revenue goals are paused because historical currencies conflict.'
                : currencyState !== 'configured'
                  ? 'Set the project currency before enabling revenue.'
                  : goal?.currency && goal.currency !== projectCurrency
                    ? `This legacy goal uses ${goal.currency}. Disable revenue explicitly before editing it.`
                    : `Revenue uses the project's permanent ${projectCurrency} currency.`}
            </FieldDescription>
          </Field>
        ) : null}
      </FieldGroup>
      {error ? <FieldError>{error}</FieldError> : null}
      <div className="flex items-center gap-2">
        <Button disabled={pending} type="submit">{goal ? 'Save changes' : 'Create goal'}</Button>
        {goal ? <Button type="button" variant="ghost" onClick={onCancel}>Cancel</Button> : null}
      </div>
    </form>
  )
}
