import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { privateRequest } from '@/features/settings/privateApi'
import { useReauth } from '@/lib/useReauth'
import { uiSettings } from '@/messages/uiSettings'

import { draftOf, emptyDraft, scheduleBody, type ExportSchedule, type ScheduleDraft } from './types'

type Site = { id: number; name: string }

export function ExportScheduleForm({ orgId, sites, schedule, open, onOpenChange, onSaved }: {
  orgId: number
  sites: Site[]
  /** The schedule being edited; null creates a new one. */
  schedule: ExportSchedule | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {open ? <FormBody orgId={orgId} sites={sites} schedule={schedule} onClose={() => onOpenChange(false)} onSaved={onSaved} /> : null}
    </Dialog>
  )
}

function FormBody({ orgId, sites, schedule, onClose, onSaved }: { orgId: number; sites: Site[]; schedule: ExportSchedule | null; onClose: () => void; onSaved: () => void }) {
  const t = uiSettings.exports.form
  const mode = schedule ? 'edit' : 'create'
  const { withReauth, reauthDialog } = useReauth()
  const [draft, setDraft] = useState<ScheduleDraft>(schedule ? draftOf(schedule) : emptyDraft)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [test, setTest] = useState<{ ok: boolean; message: string } | null>(null)
  const set = <K extends keyof ScheduleDraft>(key: K, value: ScheduleDraft[K]) => setDraft((current) => ({ ...current, [key]: value }))
  const base = `/api/private/orgs/${orgId}/export-schedules`

  async function submit(event: FormEvent) {
    event.preventDefault()
    if (mode === 'create' && draft.secret === '') {
      setError(t.secretRequired)
      return
    }
    setBusy(true)
    setError('')
    try {
      const body = scheduleBody(draft, mode)
      await withReauth(() => privateRequest(mode === 'create' ? 'POST' : 'PATCH', mode === 'create' ? base : `${base}/${schedule!.id}`, { orgId, body }))
      onSaved()
      onClose()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure))
    } finally {
      setBusy(false)
    }
  }

  async function testConnection() {
    if (!schedule) return
    setTest(null)
    try {
      const result = await withReauth(() => privateRequest<{ ok: boolean; error?: string }>('POST', `${base}/${schedule.id}/test`, { orgId }))
      setTest(result.ok ? { ok: true, message: t.testOk } : { ok: false, message: `${t.testFailed}: ${result.error ?? ''}` })
    } catch (failure) {
      setTest({ ok: false, message: `${t.testFailed}: ${failure instanceof Error ? failure.message : String(failure)}` })
    }
  }

  const text = (key: 'name' | 'endpoint' | 'region' | 'bucket' | 'prefix' | 'access_key_id', label: string, extra?: { required?: boolean; hint?: string }) => (
    <Field>
      <FieldLabel htmlFor={`export-${key}`}>{label}</FieldLabel>
      <Input id={`export-${key}`} value={draft[key]} required={extra?.required ?? true} autoComplete="off" onChange={(event) => set(key, event.target.value)} />
      {extra?.hint ? <FieldDescription>{extra.hint}</FieldDescription> : null}
    </Field>
  )

  return (
    <>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{mode === 'create' ? t.createTitle : t.editTitle}</DialogTitle>
          <DialogDescription>{uiSettings.exports.description}</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-5" onSubmit={(event) => void submit(event)}>
          <FieldGroup>
            {text('name', t.name)}
            <Field>
              <FieldLabel htmlFor="export-site">{t.site}</FieldLabel>
              <Select value={draft.site_id === null ? 'all' : String(draft.site_id)} disabled={mode === 'edit'} onValueChange={(value) => set('site_id', value === 'all' ? null : Number(value))}>
                <SelectTrigger id="export-site" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">{uiSettings.exports.allSites}</SelectItem>
                    {sites.map((site) => <SelectItem key={site.id} value={String(site.id)}>{site.name}</SelectItem>)}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="export-frequency">{t.frequency}</FieldLabel>
              <Select value={draft.frequency} onValueChange={(value) => set('frequency', value as ScheduleDraft['frequency'])}>
                <SelectTrigger id="export-frequency" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {(['daily', 'weekly', 'monthly'] as const).map((value) => <SelectItem key={value} value={value}>{uiSettings.exports.frequency[value]}</SelectItem>)}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            <Field orientation="horizontal">
              <Checkbox id="export-incremental" checked={draft.incremental} onCheckedChange={(checked) => set('incremental', checked === true)} />
              <FieldLabel htmlFor="export-incremental">{t.incremental}</FieldLabel>
            </Field>
            {text('endpoint', t.endpoint, { hint: t.endpointHint })}
            {text('region', t.region)}
            {text('bucket', t.bucket)}
            {text('prefix', t.prefix, { required: false })}
            {text('access_key_id', t.accessKey)}
            <Field>
              <FieldLabel htmlFor="export-secret">{t.secret}</FieldLabel>
              <Input id="export-secret" type="password" value={draft.secret} autoComplete="new-password" onChange={(event) => set('secret', event.target.value)} />
              <FieldDescription>{schedule?.secret_set ? t.secretSet : t.secretNew}</FieldDescription>
            </Field>
            <Field orientation="horizontal">
              <Checkbox id="export-path-style" checked={draft.path_style} onCheckedChange={(checked) => set('path_style', checked === true)} />
              <FieldLabel htmlFor="export-path-style">{t.pathStyle}</FieldLabel>
            </Field>
            {mode === 'edit' ? (
              <Field orientation="horizontal">
                <Checkbox id="export-enabled" checked={draft.enabled} onCheckedChange={(checked) => set('enabled', checked === true)} />
                <FieldLabel htmlFor="export-enabled">{t.enabled}</FieldLabel>
              </Field>
            ) : null}
          </FieldGroup>
          {error ? <FieldError>{error}</FieldError> : null}
          <div role="status" aria-live="polite" className={test?.ok ? 'text-sm text-success' : 'text-sm text-destructive'}>{test?.message ?? ''}</div>
          <DialogFooter className="sm:justify-between">
            <div className="flex flex-col gap-1">
              <Button type="button" variant="outline" disabled={mode === 'create'} onClick={() => void testConnection()}>{t.test}</Button>
              {mode === 'create' ? <span className="text-xs text-muted-foreground">{t.testNeedsSave}</span> : null}
            </div>
            <div className="flex gap-2">
              <Button type="button" variant="outline" onClick={onClose}>{t.cancel}</Button>
              <Button type="submit" disabled={busy} aria-busy={busy}>{busy ? t.saving : t.save}</Button>
            </div>
          </DialogFooter>
        </form>
      </DialogContent>
      {reauthDialog}
    </>
  )
}
