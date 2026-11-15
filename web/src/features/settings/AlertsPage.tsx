import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useGoals } from '@/features/goals/api'

import { type AlertInput, type AlertKind, type AlertRule, type AppAlertKind, type WebAlertKind, useAlerts, useDeleteAlert, useSaveAlert } from './alertsApi'
import { useChannels } from './notificationApi'

const blank: AlertInput = { kind: 'traffic_spike', threshold: 25, channel_ids: [], enabled: true }
const blankApp: AlertInput = { kind: 'app_new_issue', channel_ids: [], enabled: true }
const webKinds: WebAlertKind[] = ['traffic_spike', 'traffic_drop', 'goal_reached']
const appKinds: AppAlertKind[] = ['app_new_issue', 'app_issue_regressed', 'app_crash_free_below', 'app_issue_spike']
const labels: Record<AlertKind, string> = {
  traffic_spike: 'Traffic spike',
  traffic_drop: 'Traffic drop',
  goal_reached: 'Goal reached',
  app_new_issue: 'New issue',
  app_issue_regressed: 'Issue regressed',
  app_crash_free_below: 'Crash-free rate below threshold',
  app_issue_spike: 'Issue spike',
}
const thresholdDescriptions: Record<AlertKind, string> = {
  traffic_spike: 'Active visitors in this process during the last 60 minutes.',
  traffic_drop: 'Percentage of visitors in the same 24-hour window seven days earlier.',
  goal_reached: 'Conversions in the current local day.',
  app_new_issue: '',
  app_issue_regressed: '',
  app_crash_free_below: 'Alert when the newest release with at least 100 installs in the last 24 hours drops below this percentage (50 to 100).',
  app_issue_spike: '',
}
const kindDescriptions: Partial<Record<AlertKind, string>> = {
  app_new_issue: 'An issue is seen for the first time.',
  app_issue_regressed: 'A resolved issue comes back in a later release.',
  app_issue_spike: 'An issue has at least 10 events in the last full hour, and at least three times its average for that hour of the day over the previous week.',
}
const issueKinds = ['crash', 'error', 'hang', 'install_failed', 'install_stalled', 'incompatibility', 'operation_failed']

export function AlertsPage({ orgId, siteId, siteKind }: { orgId: number; siteId: number; siteKind?: string }) {
  const isApp = siteKind === 'app'
  const alerts = useAlerts(siteId, orgId)
  const channels = useChannels(orgId)
  const goals = useGoals(siteId)
  const save = useSaveAlert(siteId, orgId)
  const remove = useDeleteAlert(siteId, orgId)
  const [editing, setEditing] = useState<AlertRule | null | undefined>(undefined)

  return (
    <PageContainer width="narrow">
      <PageHeader
        title="Alerts"
        description={isApp ? 'Notify your team about new issues, regressions, crash-free drops and spikes.' : 'Notify your team when traffic changes or a goal reaches its threshold.'}
        actions={<Button onClick={() => setEditing(null)}>Create alert</Button>}
      />
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
              <p className="text-sm text-muted-foreground">{alert.kind.startsWith('app_') && alert.kind !== 'app_crash_free_below' ? '' : `Threshold ${alert.threshold} · `}{alert.enabled ? 'Enabled' : 'Disabled'}</p>
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
        isApp={isApp}
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

function AlertDialog({ alert, isApp, channels, goals, error, pending, onClose, onSave }: {
  alert: AlertRule | null | undefined
  isApp: boolean
  channels: { id: number; name: string; enabled: boolean }[]
  goals: { id: number; name: string }[]
  error: Error | null
  pending: boolean
  onClose: () => void
  onSave: (input: AlertInput) => Promise<void>
}) {
  const [input, setInput] = useState<AlertInput>(isApp ? blankApp : blank)
  useEffect(() => setInput(alert ? {
    kind: alert.kind,
    threshold: alert.threshold,
    goal_id: alert.goal_id,
    ...(alert.config ? { config: alert.config } : {}),
    channel_ids: alert.channel_ids,
    enabled: alert.enabled,
  } : isApp ? blankApp : blank), [alert, isApp])
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
              <select id="alert-kind" className="h-8 rounded-lg border border-input bg-background px-2 text-sm" value={input.kind} onChange={(event) => setInput((value) => changeKind(value, event.target.value as AlertKind))}>
                {(isApp ? appKinds : webKinds).map((kind) => <option key={kind} value={kind}>{labels[kind]}</option>)}
              </select>
              {kindDescriptions[input.kind] ? <FieldDescription>{kindDescriptions[input.kind]}</FieldDescription> : null}
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
            {input.kind === 'app_new_issue' ? (
              <FieldSet>
                <FieldLegend>Issue kinds (none selected means every kind)</FieldLegend>
                {issueKinds.map((kind) => (
                  <Field orientation="horizontal" key={kind}>
                    <input
                      aria-label={kind}
                      checked={input.config?.kinds?.includes(kind) ?? false}
                      id={`alert-issue-kind-${kind}`}
                      type="checkbox"
                      onChange={(event) => setInput((value) => {
                        const current = value.config?.kinds ?? []
                        const kinds = event.target.checked ? [...current, kind] : current.filter((item) => item !== kind)
                        return { ...value, config: kinds.length ? { kinds } : undefined }
                      })}
                    />
                    <FieldLabel htmlFor={`alert-issue-kind-${kind}`}>{kind}</FieldLabel>
                  </Field>
                ))}
              </FieldSet>
            ) : null}
            {input.kind === 'app_crash_free_below' ? (
              <Field>
                <FieldLabel htmlFor="alert-metric">Metric</FieldLabel>
                <select id="alert-metric" className="h-8 rounded-lg border border-input bg-background px-2 text-sm" value={input.config?.metric ?? 'users'} onChange={(event) => setInput((value) => ({ ...value, config: { metric: event.target.value as 'users' | 'sessions' } }))}>
                  <option value="users">Crash-free users</option><option value="sessions">Crash-free sessions</option>
                </select>
              </Field>
            ) : null}
            {!input.kind.startsWith('app_') || input.kind === 'app_crash_free_below' ? (
              <Field>
                <FieldLabel htmlFor="alert-threshold">{input.kind === 'app_crash_free_below' ? 'Crash-free threshold (%)' : 'Threshold'}</FieldLabel>
                <Input id="alert-threshold" min={input.kind === 'app_crash_free_below' ? '50' : '1'} max={input.kind === 'app_crash_free_below' ? '100' : undefined} step={input.kind === 'app_crash_free_below' ? '0.1' : undefined} required type="number" value={input.threshold ?? ''} onChange={(event) => setInput((value) => ({ ...value, threshold: Number(event.target.value) }))} />
                <FieldDescription>{thresholdDescriptions[input.kind]}</FieldDescription>
              </Field>
            ) : null}
            <FieldSet>
              <FieldLegend>Notification channels</FieldLegend>
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
                  <FieldLabel htmlFor={`alert-channel-${channel.id}`}>{channel.name}{channel.enabled ? '' : ' (disabled)'}</FieldLabel>
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

// changeKind switches the alert type and resets the fields that belong to one kind.
function changeKind(value: AlertInput, kind: AlertKind): AlertInput {
  const base: AlertInput = { kind, channel_ids: value.channel_ids, enabled: value.enabled }
  if (kind === 'app_crash_free_below') return { ...base, threshold: 99, config: { metric: 'users' } }
  if (kind.startsWith('app_')) return base
  return { ...base, threshold: value.threshold ?? 25 }
}
