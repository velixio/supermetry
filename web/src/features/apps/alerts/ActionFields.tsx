import { PlusIcon, XIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldDescription, FieldError, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'

import { CheckField, selectClass } from './fieldBits'
import { errorAt, type ActionType, type RuleAction } from './ruleTypes'

type Channel = { id: number; name: string; kind: string; enabled: boolean }

const actionLabels: Record<ActionType, string> = {
  channel: 'Send to channels',
  webhook: 'Call a webhook',
  assign: 'Assign the issue to a member',
  notify_owner: 'Notify the issue owner',
}

/** Metric and monitor rules have no issue, so assign and notify-owner are not offered. */
export function ActionFields({ actions, onChange, incidental, error, channels, assignees, orgSlug }: {
  actions: RuleAction[]
  onChange: (actions: RuleAction[]) => void
  incidental: boolean
  error?: string
  channels: Channel[]
  assignees: { user_id: number; name: string }[]
  orgSlug?: string
}) {
  const types = (Object.keys(actionLabels) as ActionType[]).filter((type) => !incidental || type === 'channel' || type === 'webhook')
  const update = (index: number, action: RuleAction) => onChange(actions.map((a, i) => (i === index ? action : a)))
  const add = () => onChange([...actions, { type: 'channel', channel_ids: [] }])
  return (
    <FieldSet>
      <FieldLegend>Actions</FieldLegend>
      {channels.length === 0 ? <FieldDescription>No notification channels yet. Create one in the organization notification settings{orgSlug ? ` of ${orgSlug}` : ''} first.</FieldDescription> : null}
      {actions.map((action, index) => {
        const path = `actions[${index}]`
        const message = errorAt(error, path)
        const picks = action.type === 'notify_owner' ? action.fallback_channel_ids ?? [] : action.channel_ids ?? []
        const setPicks = (ids: number[]) => update(index, action.type === 'notify_owner' ? { ...action, fallback_channel_ids: ids } : { ...action, channel_ids: ids })
        const list = action.type === 'webhook' ? channels.filter((c) => c.kind === 'webhook') : channels
        return (
          <div className="flex flex-col gap-2 rounded-lg border p-3" key={index} role="group" aria-label={`Action ${index + 1}`}>
            <div className="flex items-center gap-2">
              <select aria-label={`Action ${index + 1} type`} className={selectClass} value={action.type}
                onChange={(e) => {
                  const type = e.target.value as ActionType
                  update(index, type === 'assign' ? { type, user_id: assignees[0]?.user_id } : type === 'notify_owner' ? { type, fallback_channel_ids: [] } : { type, channel_ids: [] })
                }}>
                {types.map((type) => <option key={type} value={type}>{actionLabels[type]}</option>)}
              </select>
              {actions.length > 1 ? <Button type="button" variant="ghost" size="icon" aria-label={`Remove action ${index + 1}`} onClick={() => onChange(actions.filter((_, i) => i !== index))}><XIcon /></Button> : null}
            </div>
            {action.type === 'assign' ? (
              <Field>
                <FieldLabel htmlFor={`rule-action-${index}-user`}>Member</FieldLabel>
                <select id={`rule-action-${index}-user`} className={selectClass} value={action.user_id ?? ''} onChange={(e) => update(index, { type: 'assign', user_id: Number(e.target.value) })}>
                  <option value="" disabled>Select a member</option>
                  {assignees.map((m) => <option key={m.user_id} value={m.user_id}>{m.name}</option>)}
                </select>
              </Field>
            ) : (
              <FieldSet>
                <FieldLegend variant="label">{action.type === 'notify_owner' ? 'Channels when the issue has no owner' : action.type === 'webhook' ? 'Webhook channels' : 'Channels'}</FieldLegend>
                {action.type === 'notify_owner' ? <FieldDescription>The owner gets an email; these channels are used when nobody owns the issue.</FieldDescription> : null}
                {list.map((channel) => (
                  <CheckField key={channel.id} id={`rule-action-${index}-channel-${channel.id}`} label={`${channel.name}${channel.enabled ? '' : ' (disabled)'}`}
                    checked={picks.includes(channel.id)} onChange={(checked) => setPicks(checked ? [...picks, channel.id] : picks.filter((id) => id !== channel.id))} />
                ))}
              </FieldSet>
            )}
            {message ? <FieldError>{message}</FieldError> : null}
          </div>
        )
      })}
      {errorAt(error, 'actions') && !actions.some((_, i) => errorAt(error, `actions[${i}]`)) ? <FieldError>{errorAt(error, 'actions')}</FieldError> : null}
      <div><Button type="button" variant="outline" size="sm" onClick={add}><PlusIcon />Add action</Button></div>
    </FieldSet>
  )
}
