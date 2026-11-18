import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { type AlertRule, useAlerts } from '@/features/settings/alertsApi'
import { privateRequest } from '@/features/settings/privateApi'
import type { components } from '@/lib/api.gen'

import { CheckField, selectClass } from './fieldBits'
import { formatInZone, nextOccurrences } from './maintenance'

export type AlertSilence = components['schemas']['AlertSilence']
type SilenceInput = components['schemas']['AlertSilenceInput']

const base = (siteId: number) => `/api/private/sites/${siteId}/alert-silences`
const days = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const keyOf = (siteId: number) => ['sites', siteId, 'alert-silences'] as const

export function useSilences(siteId: number, orgId: number) {
  return useQuery({ queryKey: keyOf(siteId), queryFn: () => privateRequest<{ data: AlertSilence[] }>('GET', base(siteId), { orgId }) })
}

/** A datetime-local value (the browser's zone) as an ISO instant. */
const toIso = (local: string) => (local ? new Date(local).toISOString() : undefined)

export function describeSilence(s: AlertSilence): string {
  if (s.kind === 'silence') return `${new Date(s.starts_at).toLocaleString()} until ${s.ends_at ? new Date(s.ends_at).toLocaleString() : 'further notice'}`
  const when = (s.weekdays ?? []).map((d) => days[d]).join(', ')
  return `Every ${when} at ${s.start_time} for ${s.duration_minutes} min (${s.tz})`
}

export function SilencesPage({ siteId, orgId, siteTimezone, canEdit }: { siteId: number; orgId: number; siteTimezone: string; canEdit: boolean }) {
  const silences = useSilences(siteId, orgId)
  const alerts = useAlerts(siteId, orgId)
  const client = useQueryClient()
  const refresh = () => client.invalidateQueries({ queryKey: keyOf(siteId) })
  const create = useMutation({ mutationFn: (input: SilenceInput) => privateRequest<AlertSilence>('POST', base(siteId), { orgId, body: input }), onSuccess: refresh })
  const remove = useMutation({ mutationFn: (id: number) => privateRequest<void>('DELETE', `${base(siteId)}/${id}`, { orgId }), onSuccess: refresh })
  const rules = (alerts.data?.data ?? []).filter((a) => a.kind === 'app_rule')
  const items = silences.data?.data ?? []
  const failure = silences.error ?? remove.error

  return (
    <PageContainer width="narrow">
      <PageHeader title="Silences" description="Mute rules for a deploy or a planned outage. Muted notices are logged as suppressed and are not sent later." />
      {failure ? <p role="alert" className="text-sm text-destructive">{failure.message}</p> : null}
      {silences.data && items.length === 0 ? <p className="rounded-xl border bg-card p-5 text-sm text-muted-foreground">No silences or maintenance windows.</p> : null}
      <ul aria-label="Silences" className="flex flex-col gap-3">
        {items.map((s) => (
          <li className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card p-4" key={s.id}>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <Badge variant="secondary">{s.kind === 'silence' ? 'Silence' : 'Maintenance'}</Badge>
                <span className="font-medium">{s.alert_id ? (rules.find((r) => r.id === s.alert_id)?.name ?? `Rule ${s.alert_id}`) : 'All rules'}</span>
              </div>
              <p className="text-sm text-muted-foreground">{describeSilence(s)}{s.reason ? ` · ${s.reason}` : ''}</p>
            </div>
            {canEdit ? <Button variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(s.id)}>Delete</Button> : null}
          </li>
        ))}
      </ul>
      {canEdit ? <SilenceForm rules={rules} siteTimezone={siteTimezone} error={create.error} pending={create.isPending} onCreate={(input) => create.mutateAsync(input).then(() => undefined)} /> : null}
    </PageContainer>
  )
}

function SilenceForm({ rules, siteTimezone, error, pending, onCreate }: {
  rules: AlertRule[]
  siteTimezone: string
  error: Error | null
  pending: boolean
  onCreate: (input: SilenceInput) => Promise<void>
}) {
  const [kind, setKind] = useState<'silence' | 'maintenance'>('silence')
  const [alertId, setAlertId] = useState('')
  const [starts, setStarts] = useState('')
  const [ends, setEnds] = useState('')
  const [weekdays, setWeekdays] = useState<number[]>([0])
  const [startTime, setStartTime] = useState('02:00')
  const [duration, setDuration] = useState(120)
  const [tz, setTz] = useState(siteTimezone)
  const [reason, setReason] = useState('')
  const preview = kind === 'maintenance' ? safePreview(weekdays, startTime, duration, tz) : []

  function submit(event: FormEvent) {
    event.preventDefault()
    const input: SilenceInput = { kind, reason, ...(alertId ? { alert_id: Number(alertId) } : {}) }
    if (kind === 'silence') {
      Object.assign(input, { starts_at: toIso(starts), ends_at: toIso(ends) })
    } else {
      Object.assign(input, { weekdays, start_time: startTime, duration_minutes: duration, tz, ...(ends ? { ends_at: toIso(ends) } : {}) })
    }
    void onCreate(input).catch(() => undefined)
  }

  return (
    <form aria-label="New silence" className="flex flex-col gap-4 rounded-xl border bg-card p-4" onSubmit={submit}>
      <FieldGroup>
        <FieldLegend>New silence</FieldLegend>
        <div className="grid gap-3 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="silence-kind">Type</FieldLabel>
            <select id="silence-kind" className={selectClass} value={kind} onChange={(e) => setKind(e.target.value as 'silence' | 'maintenance')}>
              <option value="silence">One-off silence</option><option value="maintenance">Weekly maintenance window</option>
            </select>
          </Field>
          <Field>
            <FieldLabel htmlFor="silence-alert">Applies to</FieldLabel>
            <select id="silence-alert" className={selectClass} value={alertId} onChange={(e) => setAlertId(e.target.value)}>
              <option value="">All rules</option>
              {rules.map((rule) => <option key={rule.id} value={rule.id}>{rule.name}</option>)}
            </select>
          </Field>
        </div>
        {kind === 'silence' ? (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="silence-starts">Starts (optional, default now)</FieldLabel>
              <Input id="silence-starts" type="datetime-local" value={starts} onChange={(e) => setStarts(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="silence-ends">Ends</FieldLabel>
              <Input id="silence-ends" required type="datetime-local" value={ends} onChange={(e) => setEnds(e.target.value)} />
            </Field>
          </div>
        ) : (
          <>
            <FieldSet>
              <FieldLegend variant="label">Days</FieldLegend>
              <div className="flex flex-wrap gap-x-4">
                {days.map((day, index) => (
                  <CheckField key={day} id={`silence-day-${index}`} label={day} checked={weekdays.includes(index)}
                    onChange={(checked) => setWeekdays((list) => (checked ? [...list, index].sort() : list.filter((d) => d !== index)))} />
                ))}
              </div>
            </FieldSet>
            <div className="grid gap-3 sm:grid-cols-3">
              <Field>
                <FieldLabel htmlFor="silence-time">Starts at</FieldLabel>
                <Input id="silence-time" required type="time" value={startTime} onChange={(e) => setStartTime(e.target.value)} />
              </Field>
              <Field>
                <FieldLabel htmlFor="silence-duration">Duration (minutes)</FieldLabel>
                <Input id="silence-duration" required min={1} max={1440} type="number" value={duration} onChange={(e) => setDuration(Number(e.target.value))} />
              </Field>
              <Field>
                <FieldLabel htmlFor="silence-tz">Time zone</FieldLabel>
                <Input id="silence-tz" required value={tz} onChange={(e) => setTz(e.target.value)} />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="silence-until">Repeat until (optional)</FieldLabel>
              <Input id="silence-until" type="datetime-local" value={ends} onChange={(e) => setEnds(e.target.value)} />
            </Field>
            <div aria-label="Next occurrences" role="status" className="rounded-lg border bg-muted/40 p-3 text-sm">
              <p className="font-medium">Next occurrences ({tz})</p>
              {preview.length === 0 ? <p className="text-muted-foreground">Choose at least one day and a valid time zone.</p> : (
                <ul>{preview.map((o) => <li key={o.start.toISOString()}>{formatInZone(o.start, tz)} to {formatInZone(o.end, tz)}</li>)}</ul>
              )}
            </div>
          </>
        )}
        <Field>
          <FieldLabel htmlFor="silence-reason">Reason (optional)</FieldLabel>
          <Input id="silence-reason" maxLength={200} value={reason} onChange={(e) => setReason(e.target.value)} />
          <FieldDescription>Shown in the delivery history next to each suppressed notice.</FieldDescription>
        </Field>
      </FieldGroup>
      <FieldError>{error?.message}</FieldError>
      <div><Button type="submit" disabled={pending}>Create silence</Button></div>
    </form>
  )
}

function safePreview(weekdays: number[], startTime: string, duration: number, tz: string) {
  try {
    return nextOccurrences(weekdays, startTime, duration, tz, new Date())
  } catch {
    return []
  }
}
