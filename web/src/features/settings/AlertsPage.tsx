import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useGoals } from '@/features/goals/api'

import { type AlertInput, type AlertKind, type AlertRule, useAlerts, useDeleteAlert, useSaveAlert } from './alertsApi'
import { useChannels } from './notificationApi'

const blank: AlertInput = { kind: 'traffic_spike', threshold: 25, channel_ids: [], enabled: true }
const labels: Record<AlertKind, string> = {
  traffic_spike: 'Traffic spike',
  traffic_drop: 'Traffic drop',
  goal_reached: 'Goal reached',
}
const thresholdDescriptions: Record<AlertKind, string> = {
  traffic_spike: 'Active visitors in this process during the last 60 minutes.',
  traffic_drop: 'Percentage of visitors in the same 24-hour window seven days earlier.',
  goal_reached: 'Conversions in the current local day.',
}

export function AlertsPage({ orgId, siteId }: { orgId: number; siteId: number }) {
  const alerts = useAlerts(siteId, orgId)
  const channels = useChannels(orgId)
  const goals = useGoals(siteId)
  const save = useSaveAlert(siteId, orgId)
  const remove = useDeleteAlert(siteId, orgId)
  const [editing, setEditing] = useState<AlertRule | null | undefined>(undefined)

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <header className="flex items-start justify-between gap-4">
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Alerts</h1>
          <p className="text-sm text-muted-foreground">Notify your team when traffic changes or a goal reaches its threshold.</p>
        </div>
        <Button onClick={() => setEditing(null)}>Create alert</Button>
      </header>
      {alerts.error || channels.error || remove.error ? (
        <p role="alert" className="text-sm text-destructive">
          {alerts.error?.message ?? channels.error?.message ?? remove.error?.message}
        </p>
      ) : null}
      {alerts.data?.data.length === 0 ? <p className="rounded-xl border bg-card p-5 text-sm text-muted-foreground">No alerts yet.</p> : null}
      <div className="flex flex-col gap-3">
        {alerts.data?.data.map((alert) => (
          <section className="flex flex-wrap items-center justify-between gap-3 rounded-xl border bg-card p-4" key={alert.id}>
            <div>
              <h2 className="font-medium">{labels[alert.kind]}</h2>
              <p className="text-sm text-muted-foreground">Threshold {alert.threshold} · {alert.enabled ? 'Enabled' : 'Disabled'}</p>
            </div>
            <div className="flex gap-2">
              <Button variant="outline" onClick={() => setEditing(alert)}>Edit</Button>
              <Button variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(alert.id)}>Delete</Button>
            </div>
          </section>
        ))}
      </div>
      <AlertDialog
        alert={editing}
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
    </div>
  )
}

function AlertDialog({ alert, channels, goals, error, pending, onClose, onSave }: {
  alert: AlertRule | null | undefined
  channels: { id: number; name: string }[]
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
            <DialogTitle>{alert ? 'Edit alert' : 'Create alert'}</DialogTitle>
            <DialogDescription>Select a trigger and at least one notification channel.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="alert-kind">Alert type</FieldLabel>
              <select id="alert-kind" className="h-8 rounded-lg border border-input bg-background px-2 text-sm" value={input.kind} onChange={(event) => setInput((value) => ({ ...value, kind: event.target.value as AlertKind, goal_id: undefined }))}>
                <option value="traffic_spike">Traffic spike</option><option value="traffic_drop">Traffic drop</option><option value="goal_reached">Goal reached</option>
              </select>
            </Field>
            {input.kind === 'goal_reached' ? (
              <Field>
                <FieldLabel htmlFor="alert-goal">Goal</FieldLabel>
                <select id="alert-goal" required className="h-8 rounded-lg border border-input bg-background px-2 text-sm" value={input.goal_id ?? ''} onChange={(event) => setInput((value) => ({ ...value, goal_id: Number(event.target.value) }))}>
                  <option value="" disabled>Select a goal</option>
                  {goals.map((goal) => <option key={goal.id} value={goal.id}>{goal.name}</option>)}
                </select>
              </Field>
            ) : null}
            <Field>
              <FieldLabel htmlFor="alert-threshold">Threshold</FieldLabel>
              <Input id="alert-threshold" min="1" required type="number" value={input.threshold} onChange={(event) => setInput((value) => ({ ...value, threshold: Number(event.target.value) }))} />
              <FieldDescription>{thresholdDescriptions[input.kind]}</FieldDescription>
            </Field>
            <FieldSet>
              <FieldLegend>Notification channels</FieldLegend>
              {channels.map((channel) => (
                <Field orientation="horizontal" key={channel.id}>
                  <input
                    aria-label={channel.name}
                    checked={input.channel_ids.includes(channel.id)}
                    id={`alert-channel-${channel.id}`}
                    type="checkbox"
                    onChange={(event) => setInput((value) => ({
                      ...value,
                      channel_ids: event.target.checked
                        ? [...value.channel_ids, channel.id]
                        : value.channel_ids.filter((id) => id !== channel.id),
                    }))}
                  />
                  <FieldLabel htmlFor={`alert-channel-${channel.id}`}>{channel.name}</FieldLabel>
                </Field>
              ))}
            </FieldSet>
            <Field orientation="horizontal">
              <input id="alert-enabled" checked={input.enabled} type="checkbox" onChange={(event) => setInput((value) => ({ ...value, enabled: event.target.checked }))} />
              <FieldLabel htmlFor="alert-enabled">Enabled</FieldLabel>
            </Field>
          </FieldGroup>
          {input.channel_ids.length === 0 ? <FieldDescription>Select at least one channel before saving.</FieldDescription> : null}
          <FieldError>{error?.message}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={pending || input.channel_ids.length === 0}>Save alert</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
