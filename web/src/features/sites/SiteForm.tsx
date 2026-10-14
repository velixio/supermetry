import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import type { RetentionDays, WebSiteInput } from './types'

const retentionOptions: { value: RetentionDays; label: string }[] = [
  { value: 7, label: '7 days' },
  { value: 30, label: '30 days' },
  { value: 90, label: '90 days' },
  { value: 365, label: '365 days' },
  { value: 0, label: 'Forever' },
]

export function SiteForm({
  submitLabel,
  onSubmit,
  secondaryAction,
}: {
  submitLabel: string
  onSubmit: (input: WebSiteInput) => Promise<void>
  secondaryAction?: React.ReactNode
}) {
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    setSubmitting(true)
    const data = new FormData(event.currentTarget)
    try {
      await onSubmit({
        name: String(data.get('name') ?? '').trim(),
        hosts: String(data.get('domains') ?? '')
          .split(',')
          .map((host) => host.trim())
          .filter(Boolean),
        timezone: String(data.get('timezone') ?? 'UTC').trim(),
        retention_days: Number(data.get('retention')) as RetentionDays,
        revenue_currency: String(data.get('revenue_currency') ?? '').trim().toUpperCase(),
      })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The site could not be created.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form className="flex flex-col gap-5" onSubmit={(event) => void submit(event)}>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="site-name">Site name</FieldLabel>
          <Input id="site-name" name="name" autoComplete="organization" required />
        </Field>
        <Field data-invalid={Boolean(error) || undefined}>
          <FieldLabel htmlFor="site-domains">Domains</FieldLabel>
          <Input
            id="site-domains"
            name="domains"
            inputMode="url"
            placeholder="example.com, www.example.com"
            aria-invalid={Boolean(error) || undefined}
            required
          />
          <FieldDescription>Comma-separated hostnames, without a scheme or path.</FieldDescription>
          <FieldError>{error}</FieldError>
        </Field>
        <TimezoneSelect />
        <Field>
          <FieldLabel htmlFor="site-retention">Retention</FieldLabel>
          <select
            id="site-retention"
            name="retention"
            defaultValue="30"
            className="h-9 rounded-lg border bg-background px-3 text-sm"
          >
            {retentionOptions.map((option) => (
              <option key={option.value} value={option.value}>{option.label}</option>
            ))}
          </select>
        </Field>
        <Field>
          <FieldLabel htmlFor="site-revenue-currency">Revenue currency</FieldLabel>
          <Input
            id="site-revenue-currency"
            name="revenue_currency"
            autoCapitalize="characters"
            maxLength={3}
            pattern="[A-Za-z]{3}"
            placeholder="USD"
            required
          />
          <FieldDescription>
            This three-letter currency applies to all project revenue and cannot be changed after creation.
          </FieldDescription>
        </Field>
      </FieldGroup>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={submitting}>
          {submitting ? 'Creating…' : submitLabel}
        </Button>
        {secondaryAction}
      </div>
    </form>
  )
}

export function TimezoneSelect({ value, onChange }: { value?: string; onChange?: (value: string) => void }) {
  return (
    <Field>
      <FieldLabel htmlFor="site-timezone">Time zone</FieldLabel>
      <Input
        id="site-timezone"
        name="timezone"
        value={value}
        defaultValue={value === undefined ? Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC' : undefined}
        onChange={onChange ? (event) => onChange(event.target.value) : undefined}
        required
      />
    </Field>
  )
}
