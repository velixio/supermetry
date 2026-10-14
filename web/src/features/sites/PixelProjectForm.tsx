import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { TimezoneSelect } from '@/features/sites/SiteForm'
import type { PixelSiteInput } from '@/features/sites/types'
import { en } from '@/messages/en'

export function PixelProjectForm({ submitLabel, onSubmit }: { submitLabel: string; onSubmit: (input: PixelSiteInput) => Promise<void> }) {
  const [name, setName] = useState('')
  const [timezone, setTimezone] = useState(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await onSubmit({ kind: 'pixel', name: name.trim(), timezone, retention_days: 365 })
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The project could not be created.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="flex flex-col gap-5" onSubmit={(event) => void submit(event)}>
      <FieldGroup>
        <Field data-invalid={Boolean(error) || undefined}>
          <FieldLabel htmlFor="pixel-project-name">{en.pixels.projectName}</FieldLabel>
          <Input id="pixel-project-name" required maxLength={120} value={name} onChange={(event) => setName(event.target.value)} />
          <FieldError>{error}</FieldError>
        </Field>
        <TimezoneSelect value={timezone} onChange={setTimezone} />
      </FieldGroup>
      <Button type="submit" disabled={busy || name.trim() === ''}>{busy ? 'Creating…' : submitLabel}</Button>
    </form>
  )
}
