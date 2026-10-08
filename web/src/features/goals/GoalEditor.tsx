import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import type { Goal, GoalInput } from './types'

type GoalEditorProps = {
  goal?: Goal
  pending: boolean
  onSave: (input: GoalInput) => void | Promise<void>
  onCancel?: () => void
}

const emptyGoal: GoalInput = { kind: 'page', match: 'exact', value: '', name: '' }
const commonCurrencies = ['USD', 'EUR', 'GBP', 'INR', 'JPY', 'CAD', 'AUD'] as const

function isCommonCurrency(currency: string | undefined): boolean {
  return commonCurrencies.some((value) => value === currency)
}

export function GoalEditor({ goal, pending, onSave, onCancel }: GoalEditorProps) {
  const [input, setInput] = useState<GoalInput>(goal ?? emptyGoal)
  const [customCurrency, setCustomCurrency] = useState(Boolean(goal?.currency && !isCommonCurrency(goal.currency)))
  const [error, setError] = useState('')

  useEffect(() => {
    setInput(goal ?? emptyGoal)
    setCustomCurrency(Boolean(goal?.currency && !isCommonCurrency(goal.currency)))
  }, [goal])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    const currency = input.currency?.trim().toUpperCase()
    if (!input.name.trim() || !input.value.trim()) {
      setError('Enter a name and value.')
      return
    }
    if (input.kind === 'event' && customCurrency && !/^[A-Z]{3}$/.test(currency ?? '')) {
      setError('Enter a three-letter currency code.')
      return
    }
    setError('')
    const normalized = { ...input, name: input.name.trim(), value: input.value.trim() }
    if (input.kind === 'event' && currency) normalized.currency = currency
    else delete normalized.currency
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
            setCustomCurrency(false)
            setInput((current) => kind === 'page' ? { ...current, kind, currency: undefined } : { ...current, kind })
          }}>
            <option value="page">Page</option><option value="event">Event</option>
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
        {input.kind === 'event' ? (
          <Field data-invalid={error !== '' && customCurrency && !/^[A-Za-z]{3}$/.test(input.currency ?? '')}>
            <FieldLabel htmlFor="goal-currency">Revenue currency</FieldLabel>
            <select
              id="goal-currency"
              aria-invalid={error !== '' && customCurrency && !/^[A-Za-z]{3}$/.test(input.currency ?? '')}
              className="h-8 rounded-lg border border-input bg-background px-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
              value={customCurrency ? 'custom' : (input.currency ?? '')}
              onChange={(event) => {
                const currency = event.target.value
                setCustomCurrency(currency === 'custom')
                setInput((current) => ({ ...current, currency: currency === 'custom' || currency === '' ? undefined : currency }))
              }}
            >
              <option value="">No revenue</option>
              {commonCurrencies.map((currency) => <option key={currency} value={currency}>{currency}</option>)}
              <option value="custom">Other currency…</option>
            </select>
            {customCurrency ? (
              <Input
                id="goal-custom-currency"
                aria-invalid={error !== '' && !/^[A-Za-z]{3}$/.test(input.currency ?? '')}
                aria-label="Custom currency"
                autoCapitalize="characters"
                maxLength={3}
                placeholder="CHF"
                value={input.currency ?? ''}
                onChange={(event) => setInput((current) => ({ ...current, currency: event.target.value }))}
              />
            ) : null}
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
