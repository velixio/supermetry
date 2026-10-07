import { useEffect, useState, type FormEvent } from 'react'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import type { FunnelStep } from './types'

const blankStep = (): FunnelStep => ({ type: 'page', match: 'exact', value: '' })
const defaultSteps = [blankStep()]

type FunnelBuilderProps = {
  initialSteps?: FunnelStep[]
  onSubmit: (steps: FunnelStep[]) => void | Promise<void>
  pending?: boolean
}

export function FunnelBuilder({
  initialSteps = defaultSteps,
  onSubmit,
  pending = false,
}: FunnelBuilderProps) {
  const [steps, setSteps] = useState<FunnelStep[]>(initialSteps)
  const [error, setError] = useState('')

  useEffect(() => {
    setSteps(initialSteps.map((step) => ({ ...step })))
    setError('')
  }, [initialSteps])

  const update = (index: number, patch: Partial<FunnelStep>) => {
    setSteps((current) =>
      current.map((step, stepIndex) =>
        stepIndex === index ? { ...step, ...patch } : step,
      ),
    )
  }
  const move = (index: number, offset: -1 | 1) => {
    setSteps((current) => {
      const destination = index + offset
      if (destination < 0 || destination >= current.length) return current
      const next = [...current]
      const [step] = next.splice(index, 1)
      if (!step) return current
      next.splice(destination, 0, step)
      return next
    })
  }
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (steps.length < 2) {
      setError('Add at least two steps.')
      return
    }
    if (steps.some((step) => step.value.trim() === '')) {
      setError('Every step needs a value.')
      return
    }
    setError('')
    void onSubmit(steps.map((step) => ({ ...step, value: step.value.trim() })))
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <FieldGroup>
        {steps.map((step, index) => (
          <Field key={index}>
            <div className="flex items-center justify-between gap-3">
              <FieldLabel htmlFor={`funnel-step-${index}`}>Step {index + 1}</FieldLabel>
              <div className="flex items-center gap-1">
                <Button
                  aria-label={`Move step ${index + 1} up`}
                  disabled={index === 0}
                  size="icon-sm"
                  type="button"
                  variant="ghost"
                  onClick={() => move(index, -1)}
                >
                  <ArrowUp />
                </Button>
                <Button
                  aria-label={`Move step ${index + 1} down`}
                  disabled={index === steps.length - 1}
                  size="icon-sm"
                  type="button"
                  variant="ghost"
                  onClick={() => move(index, 1)}
                >
                  <ArrowDown />
                </Button>
                <Button
                  aria-label={`Remove step ${index + 1}`}
                  disabled={steps.length === 1}
                  size="icon-sm"
                  type="button"
                  variant="ghost"
                  onClick={() =>
                    setSteps((current) =>
                      current.filter((_, stepIndex) => stepIndex !== index),
                    )
                  }
                >
                  <Trash2 />
                </Button>
              </div>
            </div>
            <div className="grid grid-cols-[minmax(6rem,0.7fr)_minmax(7rem,0.8fr)_minmax(0,2fr)] gap-2 max-sm:grid-cols-1">
              <select
                aria-label={`Step ${index + 1} type`}
                className="h-8 rounded-lg border border-input bg-background px-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                value={step.type}
                onChange={(event) =>
                  update(index, { type: event.target.value as FunnelStep['type'] })
                }
              >
                <option value="page">Page</option>
                <option value="event">Event</option>
              </select>
              <select
                aria-label={`Step ${index + 1} match`}
                className="h-8 rounded-lg border border-input bg-background px-2 text-sm outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
                value={step.match}
                onChange={(event) =>
                  update(index, { match: event.target.value as FunnelStep['match'] })
                }
              >
                <option value="exact">Exact</option>
                <option value="prefix">Prefix</option>
                <option value="wildcard">Wildcard</option>
              </select>
              <Input
                id={`funnel-step-${index}`}
                aria-invalid={error !== '' && step.value.trim() === ''}
                placeholder={step.type === 'page' ? '/checkout' : 'signup'}
                value={step.value}
                onChange={(event) => update(index, { value: event.target.value })}
              />
            </div>
          </Field>
        ))}
      </FieldGroup>
      {error ? <FieldError>{error}</FieldError> : null}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          onClick={() => setSteps((current) => [...current, blankStep()])}
        >
          <Plus data-icon="inline-start" />
          Add step
        </Button>
        <Button disabled={pending} type="submit">
          Save funnel
        </Button>
      </div>
    </form>
  )
}
