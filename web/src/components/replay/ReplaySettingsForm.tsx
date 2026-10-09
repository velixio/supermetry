import { useEffect, useState, type FormEvent } from 'react'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import type { SiteSettings } from '@/lib/replay-api'

function ReplayStatus({ settings }: { settings: SiteSettings }) {
  return (
    <Field>
      <FieldLabel>Effective recording</FieldLabel>
      <Badge className="self-start" variant={settings.enabled ? 'default' : 'secondary'}>{settings.enabled ? 'On' : 'Off'}</Badge>
      <FieldDescription>Project opt-in: {settings.configured_enabled ? 'on' : 'off'}. Instance permission: {settings.instance_enabled ? 'on' : 'off'}.</FieldDescription>
      {settings.instance_enabled === false ? <FieldDescription>Instance replay is off. An instance administrator must enable replay and restart the server before a project can opt in.</FieldDescription> : null}
      {settings.restrictions?.includes('project_replay_disabled') ? <FieldDescription>This project has not opted in to recording.</FieldDescription> : null}
      {settings.instance_enabled === undefined ? <FieldDescription>Instance policy is unavailable. Reload settings before enabling replay.</FieldDescription> : null}
      <FieldDescription>Recording requires both permissions and visitor consent rules. Identified tracking alone does not enable replay. Changes below take effect after saving.</FieldDescription>
    </Field>
  )
}

function ConstraintNote({ setting }: { setting: SiteSettings['sample_rate'] }) {
  return <>
    {setting.configured !== undefined ? <FieldDescription>Configured: {setting.configured}. Effective: {setting.value}.</FieldDescription> : null}
    {setting.maximum !== undefined ? <FieldDescription>Instance maximum: {setting.maximum}. This project can choose a lower value.</FieldDescription> : null}
  </>
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
    if (editable && !saving) onSave(draft)
  }

  const updateTrigger = (key: keyof Omit<SiteSettings['triggers'], 'url_patterns'>, value: boolean) =>
    setDraft((current) => ({ ...current, triggers: { ...current.triggers, [key]: value } }))

  return (
    <form onSubmit={submit}>
      <Card>
        <CardHeader>
          <CardTitle>Session replay</CardTitle>
          <CardDescription>Capture sampled browser sessions within instance privacy limits.</CardDescription>
        </CardHeader>
        <CardContent>
          <FieldGroup>
            <ReplayStatus settings={settings} />
            <ToggleField
              checked={draft.configured_enabled ?? false}
              disabled={!editable || saving || (settings.instance_enabled !== true && !draft.configured_enabled)}
              label="Enable session replay for this project"
              onCheckedChange={(configured_enabled) => setDraft((current) => ({ ...current, configured_enabled }))}
            />
            <Field data-disabled={draft.mask.locked || !editable || undefined}>
              <ToggleField
                checked={draft.mask.value}
                disabled={draft.mask.locked || !editable}
                label="Mask page content"
                onCheckedChange={(value) => setDraft((current) => ({ ...current, mask: { ...current.mask, value } }))}
              />
              {settings.mask.configured !== undefined ? <FieldDescription>Configured masking: {settings.mask.configured ? 'on' : 'off'}. Effective masking: {settings.mask.value ? 'on' : 'off'}.</FieldDescription> : null}
              {draft.mask.locked ? <FieldDescription>Masking is required by the instance.</FieldDescription> : null}
            </Field>
            <Field data-disabled={!editable || saving || undefined}>
              <FieldLabel htmlFor="replay-sample-rate">Sample rate (%)</FieldLabel>
              <Input
                id="replay-sample-rate"
                type="number"
                min={0}
                max={settings.sample_rate.maximum ?? 100}
                value={draft.sample_rate.value}
                disabled={!editable || saving}
                onChange={(event) => setDraft((current) => ({ ...current, sample_rate: { ...current.sample_rate, value: Number(event.target.value) } }))}
              />
              <ConstraintNote setting={settings.sample_rate} />
            </Field>
            <Field data-disabled={!editable || saving || undefined}>
              <FieldLabel htmlFor="replay-retention">Retention (days)</FieldLabel>
              <Input
                id="replay-retention"
                type="number"
                max={settings.retention_days.maximum ?? 365}
                min={1}
                value={draft.retention_days.value}
                disabled={!editable || saving}
                onChange={(event) => setDraft((current) => ({ ...current, retention_days: { ...current.retention_days, value: Number(event.target.value) } }))}
              />
              <ConstraintNote setting={settings.retention_days} />
            </Field>
            <Field data-disabled={!editable || saving || undefined}>
              <FieldLabel htmlFor="replay-quota">Quota (bytes)</FieldLabel>
              <Input
                id="replay-quota"
                type="number"
                max={settings.quota_bytes.maximum}
                min={1}
                value={draft.quota_bytes.value}
                disabled={!editable || saving}
                onChange={(event) => setDraft((current) => ({ ...current, quota_bytes: { ...current.quota_bytes, value: Number(event.target.value) } }))}
              />
              <ConstraintNote setting={settings.quota_bytes} />
            </Field>
            <FieldSet>
              <FieldLegend>Recording triggers</FieldLegend>
              <FieldDescription>Triggered capture is unavailable under the sampling ceiling; enabled sampled sessions are recorded in full. Stored trigger settings are preserved.</FieldDescription>
              <ToggleField checked={draft.triggers.error} disabled label="JavaScript errors" onCheckedChange={(value) => updateTrigger('error', value)} />
              <ToggleField checked={draft.triggers.rage_click} disabled label="Rage clicks" onCheckedChange={(value) => updateTrigger('rage_click', value)} />
              <ToggleField checked={draft.triggers.url} disabled label="Matching URLs" onCheckedChange={(value) => updateTrigger('url', value)} />
              <ToggleField checked={draft.triggers.manual} disabled label="Manual trigger" onCheckedChange={(value) => updateTrigger('manual', value)} />
            </FieldSet>
            <Field>
              <FieldLabel htmlFor="replay-url-patterns">URL patterns</FieldLabel>
              <Textarea
                id="replay-url-patterns"
                disabled
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
