import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Link } from '@tanstack/react-router'
import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useGoals } from '@/features/goals/api'
import { uiAlerts as t } from '@/messages/uiAlerts'

import { type AlertInput, type AlertKind, type AlertRule, type WebAlertKind, useAlerts, useDeleteAlert, useSaveAlert } from './alertsApi'
import { useChannels } from './notificationApi'

const blank: AlertInput = { kind: 'traffic_spike', threshold: 25, channel_ids: [], enabled: true }
const webKinds: WebAlertKind[] = ['traffic_spike', 'traffic_drop', 'goal_reached']
const labels: Record<WebAlertKind, string> = t.alerts.kinds
const thresholdDescriptions: Record<WebAlertKind, string> = t.alerts.thresholdNotes

export function AlertsPage({ orgId, orgSlug, siteId }: { orgId: number; orgSlug?: string; siteId: number }) {
  const alerts = useAlerts(siteId, orgId)
  const channels = useChannels(orgId)
  const goals = useGoals(siteId)
  const save = useSaveAlert(siteId, orgId)
  const remove = useDeleteAlert(siteId, orgId)
  const [editing, setEditing] = useState<AlertRule | null | undefined>(undefined)

  return (
    <PageContainer width="narrow">
      <PageHeader
        title={t.alerts.title}
        actions={<Button onClick={() => setEditing(null)}>{t.alerts.create}</Button>}
      />
      {alerts.error || channels.error || remove.error ? (
        <p role="alert" className="text-sm text-destructive">
          {alerts.error?.message ?? channels.error?.message ?? remove.error?.message}
        </p>
      ) : null}
      {alerts.data?.data.length === 0 ? <p className="rounded-md bg-muted/50 p-4 text-sm text-muted-foreground">{t.alerts.empty}</p> : null}
      {alerts.data && alerts.data.data.length > 0 ? <ul className="flex flex-col divide-y rounded-lg border bg-card px-4">
        {alerts.data.data.map((alert) => (
          <li className="flex flex-wrap items-center justify-between gap-3 py-3" key={alert.id}>
            <div>
              <h2 className="text-sm font-medium">{labels[alert.kind as WebAlertKind] ?? alert.kind}</h2>
              <p className="text-sm text-muted-foreground">{t.alerts.threshold(alert.threshold ?? '')} · {alert.enabled ? t.common.enabled : t.common.disabled}</p>
            </div>
            <div className="flex gap-2">
              <Button variant="secondary" size="sm" className="max-md:min-h-11" onClick={() => setEditing(alert)}>{t.common.edit}</Button>
              <Button variant="secondary" size="sm" className="max-md:min-h-11" disabled={remove.isPending} onClick={() => remove.mutate(alert.id)}>{t.common.delete}</Button>
            </div>
          </li>
        ))}
      </ul> : null}
      <AlertDialog
        alert={editing}
        orgSlug={orgSlug}
        channels={channels.data?.data ?? []}
        goals={goals.data ?? []}
        error={save.error}
        pending={save.isPending}
        onClose={() => setEditing(undefined)}
        onSave={async (input) => {
          await save.mutateAsync({ id: editing?.id, input })
          setEditing(undefined)
        }}
      />
    </PageContainer>
  )
}

function AlertDialog({ alert, orgSlug, channels, goals, error, pending, onClose, onSave }: {
  alert: AlertRule | null | undefined
  orgSlug?: string
  channels: { id: number; name: string; enabled: boolean }[]
  goals: { id: number; name: string }[]
  error: Error | null
  pending: boolean
  onClose: () => void
  onSave: (input: AlertInput) => Promise<void>
}) {
  const [input, setInput] = useState<AlertInput>(blank)
  useEffect(() => setInput(alert ? {
    kind: alert.kind,
    threshold: alert.threshold,
    goal_id: alert.goal_id,
    ...(alert.config ? { config: alert.config } : {}),
    channel_ids: alert.channel_ids,
    enabled: alert.enabled,
  } : blank), [alert])
  if (alert === undefined) return null

  function submit(event: FormEvent) {
    event.preventDefault()
    void onSave(input).catch(() => undefined)
  }
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent>
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{alert ? t.alerts.editTitle : t.alerts.create}</DialogTitle>
            <DialogDescription>{t.alerts.dialogNote}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="alert-kind">{t.alerts.type}</FieldLabel>
              <select id="alert-kind" className="h-9 rounded-md border border-input bg-background px-2 text-sm" value={input.kind} onChange={(event) => setInput((value) => changeKind(value, event.target.value as AlertKind))}>
                {webKinds.map((kind) => <option key={kind} value={kind}>{labels[kind]}</option>)}
              </select>
            </Field>
            {input.kind === 'goal_reached' ? (
              <Field>
                <FieldLabel htmlFor="alert-goal">{t.alerts.goal}</FieldLabel>
                <select id="alert-goal" required className="h-9 rounded-md border border-input bg-background px-2 text-sm" value={input.goal_id ?? ''} onChange={(event) => setInput((value) => ({ ...value, goal_id: Number(event.target.value) }))}>
                  <option value="" disabled>{t.alerts.selectGoal}</option>
                  {goals.map((goal) => <option key={goal.id} value={goal.id}>{goal.name}</option>)}
                </select>
              </Field>
            ) : null}
            <Field>
              <FieldLabel htmlFor="alert-threshold">{t.alerts.thresholdLabel}</FieldLabel>
              <Input id="alert-threshold" min="1" required type="number" value={input.threshold ?? ''} onChange={(event) => setInput((value) => ({ ...value, threshold: Number(event.target.value) }))} />
              <FieldDescription>{thresholdDescriptions[input.kind as WebAlertKind]}</FieldDescription>
            </Field>
            <FieldSet>
              <FieldLegend>{t.alerts.channels}</FieldLegend>
              {channels.length === 0 ? (
                <FieldDescription>
                  {t.alerts.noChannels}{orgSlug ? <> <Link className="underline" to="/app/$org/settings/notifications" params={{ org: orgSlug }}>{t.alerts.createChannelLink}</Link> {t.alerts.createChannelFirst}</> : ` ${t.alerts.createChannelElsewhere}`}
                </FieldDescription>
              ) : null}
              {channels.map((channel) => (
                <Field orientation="horizontal" key={channel.id}>
                  <input
                    aria-label={channel.name}
                    checked={input.channel_ids.includes(channel.id)}
                    disabled={!channel.enabled && !input.channel_ids.includes(channel.id)}
                    id={`alert-channel-${channel.id}`}
                    type="checkbox"
                    onChange={(event) => setInput((value) => ({
                      ...value,
                      channel_ids: event.target.checked
                        ? [...value.channel_ids, channel.id]
                        : value.channel_ids.filter((id) => id !== channel.id),
                    }))}
                  />
                  <FieldLabel htmlFor={`alert-channel-${channel.id}`}>{channel.name}{channel.enabled ? '' : t.common.disabledSuffix}</FieldLabel>
                </Field>
              ))}
            </FieldSet>
            <Field orientation="horizontal">
              <input id="alert-enabled" checked={input.enabled} type="checkbox" onChange={(event) => setInput((value) => ({ ...value, enabled: event.target.checked }))} />
              <FieldLabel htmlFor="alert-enabled">{t.common.enabled}</FieldLabel>
            </Field>
          </FieldGroup>
          {input.channel_ids.length === 0 ? <FieldDescription>{t.alerts.pickChannel}</FieldDescription> : null}
          <FieldError>{error?.message}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>{t.common.cancel}</Button>
            <Button type="submit" disabled={pending || input.channel_ids.length === 0}>{t.alerts.save}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// changeKind switches the alert type and keeps the threshold.
function changeKind(value: AlertInput, kind: AlertKind): AlertInput {
  return { kind, channel_ids: value.channel_ids, enabled: value.enabled, threshold: value.threshold ?? 25 }
}
