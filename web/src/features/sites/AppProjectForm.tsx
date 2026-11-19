import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { TimezoneSelect, retentionLabel } from '@/features/sites/SiteForm'
import type { AppSiteInput, RetentionDays } from '@/features/sites/types'
import { retentionOptions, retentionPolicy, type RetentionPolicy } from '@/lib/instanceConfig'
import { en } from '@/messages/en'

const selectClass = 'h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm'

export function AppProjectForm({ submitLabel, onSubmit, retention }: {
  submitLabel: string
  onSubmit: (input: AppSiteInput) => Promise<void>
  retention?: RetentionPolicy
}) {
  const policy = retention ?? { ...retentionPolicy(undefined), forever: false }
  const [name, setName] = useState('')
  const [timezone, setTimezone] = useState(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
  const [days, setDays] = useState(90)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await onSubmit({ kind: 'app', name: name.trim(), timezone, retention_days: days as RetentionDays })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The app could not be created.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="flex flex-col gap-5" onSubmit={(event) => void submit(event)}>
      <FieldGroup>
        <Field data-invalid={Boolean(error) || undefined}>
          <FieldLabel htmlFor="app-project-name">App name</FieldLabel>
          <Input id="app-project-name" required maxLength={120} value={name} onChange={(event) => setName(event.target.value)} />
          <FieldError>{error}</FieldError>
        </Field>
        <TimezoneSelect value={timezone} onChange={setTimezone} />
        <Field>
          <FieldLabel htmlFor="app-retention">{en.pixels.appRetention}</FieldLabel>
          <select id="app-retention" className={selectClass} value={days} onChange={(event) => setDays(Number(event.target.value))}>
            {retentionOptions(policy).map((option) => (
              <option key={option} value={option}>{retentionLabel(option)}</option>
            ))}
          </select>
        </Field>
      </FieldGroup>
      <Button type="submit" disabled={busy || name.trim() === ''}>{busy ? 'Creating…' : submitLabel}</Button>
    </form>
  )
}
