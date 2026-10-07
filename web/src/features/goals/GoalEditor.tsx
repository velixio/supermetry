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

export function GoalEditor({ goal, pending, onSave, onCancel }: GoalEditorProps) {
  const [input, setInput] = useState<GoalInput>(goal ?? emptyGoal)
  const [error, setError] = useState('')

  useEffect(() => setInput(goal ?? emptyGoal), [goal])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!input.name.trim() || !input.value.trim()) {
      setError('Enter a name and value.')
      return
    }
    setError('')
    void onSave({ ...input, name: input.name.trim(), value: input.value.trim() })
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
          <select id="goal-kind" className="h-8 rounded-lg border border-input bg-background px-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50" value={input.kind} onChange={(event) => setInput((current) => ({ ...current, kind: event.target.value as GoalInput['kind'] }))}>
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
      </FieldGroup>
      {error ? <FieldError>{error}</FieldError> : null}
      <div className="flex items-center gap-2">
        <Button disabled={pending} type="submit">{goal ? 'Save changes' : 'Create goal'}</Button>
        {goal ? <Button type="button" variant="ghost" onClick={onCancel}>Cancel</Button> : null}
      </div>
    </form>
  )
}
