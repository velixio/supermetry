import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import type { SiteSettings } from '@/lib/replay-api'

function LockedNote({ locked }: { locked: boolean }) {
  return locked ? <FieldDescription>Set by environment variable</FieldDescription> : null
}

function ToggleField({
  checked,
  disabled,
  label,
  onCheckedChange,
}: {
  checked: boolean
  disabled?: boolean
  label: string
  onCheckedChange: (checked: boolean) => void
}) {
  return (
    <Field data-disabled={disabled || undefined} orientation="horizontal">
      <FieldLabel htmlFor={`replay-${label.toLowerCase().replaceAll(' ', '-')}`}>{label}</FieldLabel>
      <Switch
        id={`replay-${label.toLowerCase().replaceAll(' ', '-')}`}
        aria-label={label}
        checked={checked}
        disabled={disabled}
        onCheckedChange={onCheckedChange}
      />
    </Field>
  )
}

export function ReplaySettingsForm({
  settings,
  saving,
  editable = true,
  onSave,
}: {
  settings: SiteSettings
  saving: boolean
  editable?: boolean
  onSave: (settings: SiteSettings) => void
}) {
  const [draft, setDraft] = useState(settings)
  useEffect(() => setDraft(settings), [settings])

  function submit(event: FormEvent) {
    event.preventDefault()
    onSave(draft)
  }

  const updateTrigger = (key: keyof Omit<SiteSettings['triggers'], 'url_patterns'>, value: boolean) =>
    setDraft((current) => ({ ...current, triggers: { ...current.triggers, [key]: value } }))

  return (
    <form onSubmit={submit}>
      <Card>
        <CardHeader>
          <CardTitle>Session replay</CardTitle>
          <CardDescription>Capture sampled browser sessions with privacy controls and explicit triggers.</CardDescription>
        </CardHeader>
        <CardContent>
          <FieldGroup>
            <ToggleField
              checked={draft.enabled}
              disabled={!editable}
              label="Enable session replay"
              onCheckedChange={(enabled) => setDraft((current) => ({ ...current, enabled }))}
            />
            <Field data-disabled={draft.mask.locked || !editable || undefined}>
              <ToggleField
                checked={draft.mask.value}
                disabled={draft.mask.locked || !editable}
                label="Mask page content"
                onCheckedChange={(value) => setDraft((current) => ({ ...current, mask: { ...current.mask, value } }))}
              />
              <LockedNote locked={draft.mask.locked} />
            </Field>
            <Field data-disabled={draft.sample_rate.locked || !editable || undefined}>
              <FieldLabel htmlFor="replay-sample-rate">Sample rate (%)</FieldLabel>
              <Input
                id="replay-sample-rate"
                type="number"
                min={0}
                max={100}
                value={draft.sample_rate.value}
                disabled={draft.sample_rate.locked || !editable}
                onChange={(event) => setDraft((current) => ({ ...current, sample_rate: { ...current.sample_rate, value: Number(event.target.value) } }))}
              />
              <LockedNote locked={draft.sample_rate.locked} />
            </Field>
            <Field data-disabled={draft.retention_days.locked || !editable || undefined}>
              <FieldLabel htmlFor="replay-retention">Retention (days)</FieldLabel>
              <Input
                id="replay-retention"
                type="number"
                min={1}
                value={draft.retention_days.value}
                disabled={draft.retention_days.locked || !editable}
                onChange={(event) => setDraft((current) => ({ ...current, retention_days: { ...current.retention_days, value: Number(event.target.value) } }))}
              />
              <LockedNote locked={draft.retention_days.locked} />
            </Field>
            <Field data-disabled={draft.quota_bytes.locked || !editable || undefined}>
              <FieldLabel htmlFor="replay-quota">Quota (bytes)</FieldLabel>
              <Input
                id="replay-quota"
                type="number"
                min={0}
                value={draft.quota_bytes.value}
                disabled={draft.quota_bytes.locked || !editable}
                onChange={(event) => setDraft((current) => ({ ...current, quota_bytes: { ...current.quota_bytes, value: Number(event.target.value) } }))}
              />
              <LockedNote locked={draft.quota_bytes.locked} />
            </Field>
            <FieldSet>
              <FieldLegend>Recording triggers</FieldLegend>
              <ToggleField checked={draft.triggers.error} disabled={!editable} label="JavaScript errors" onCheckedChange={(value) => updateTrigger('error', value)} />
              <ToggleField checked={draft.triggers.rage_click} disabled={!editable} label="Rage clicks" onCheckedChange={(value) => updateTrigger('rage_click', value)} />
              <ToggleField checked={draft.triggers.url} disabled={!editable} label="Matching URLs" onCheckedChange={(value) => updateTrigger('url', value)} />
              <ToggleField checked={draft.triggers.manual} disabled={!editable} label="Manual trigger" onCheckedChange={(value) => updateTrigger('manual', value)} />
            </FieldSet>
            <Field>
              <FieldLabel htmlFor="replay-url-patterns">URL patterns</FieldLabel>
              <Textarea
                id="replay-url-patterns"
                disabled={!editable}
                placeholder="/checkout/*"
                value={draft.triggers.url_patterns.join('\n')}
                onChange={(event) => setDraft((current) => ({
                  ...current,
                  triggers: { ...current.triggers, url_patterns: event.target.value.split('\n').map((value) => value.trim()).filter(Boolean) },
                }))}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="replay-consent">Consent</FieldLabel>
              <select
                id="replay-consent"
                className="h-8 rounded-lg border border-input bg-transparent px-2.5 text-sm"
                disabled={!editable}
                value={draft.consent}
                onChange={(event) => setDraft((current) => ({ ...current, consent: event.target.value as SiteSettings['consent'] }))}
              >
                <option value="auto">Automatic</option>
                <option value="always">Always require consent</option>
                <option value="never">Never record</option>
              </select>
            </Field>
            {editable ? <Button disabled={saving} type="submit">{saving ? 'Saving…' : 'Save replay settings'}</Button> : null}
          </FieldGroup>
        </CardContent>
      </Card>
    </form>
  )
}
