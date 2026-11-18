import type { ReactNode } from 'react'

import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

export const selectClass = 'h-8 rounded-lg border border-input bg-background px-2 text-sm'

/** A labelled number input that shows the API error found at its path. */
export function NumberField({ id, label, value, onChange, error, min, step, description, required }: {
  id: string
  label: string
  value: number | undefined
  onChange: (value: number | undefined) => void
  error?: string
  min?: number
  step?: number
  description?: ReactNode
  required?: boolean
}) {
  return (
    <Field data-invalid={error ? true : undefined}>
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Input id={id} aria-invalid={error ? true : undefined} min={min} required={required} step={step} type="number" value={value ?? ''}
        onChange={(event) => onChange(event.target.value === '' ? undefined : Number(event.target.value))} />
      {description ? <FieldDescription>{description}</FieldDescription> : null}
      {error ? <FieldError>{error}</FieldError> : null}
    </Field>
  )
}

export function CheckField({ id, label, checked, onChange, disabled }: { id: string; label: string; checked: boolean; onChange: (checked: boolean) => void; disabled?: boolean }) {
  return (
    <Field orientation="horizontal">
      <input id={id} checked={checked} disabled={disabled} type="checkbox" onChange={(event) => onChange(event.target.checked)} />
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
    </Field>
  )
}

/** "a, b" to ["a", "b"], and back. */
export const splitList = (text: string): string[] => text.split(',').map((item) => item.trim()).filter(Boolean)
