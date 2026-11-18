import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import type { AlertInput, AlertRule } from '@/features/settings/alertsApi'

import { ActionFields } from './ActionFields'
import { CheckField, NumberField } from './fieldBits'
import { FilterFields } from './FilterFields'
import { blankRule, errorAt, isIncidental, parseFieldError, type RuleDoc, type TriggerType } from './ruleTypes'
import { TriggerFields } from './TriggerFields'

type Channel = { id: number; name: string; kind: string; enabled: boolean }

const handledPaths = ['name', 'trigger', 'filters', 'actions', 'frequency_minutes']

/** Drops empty lists and half-typed tag filters before the rule is sent. */
export function cleanRule(rule: RuleDoc): RuleDoc {
  const filters = { ...rule.filters }
  for (const key of ['environments', 'releases', 'platforms', 'levels', 'kinds'] as const) {
    if (!filters[key]?.length) delete filters[key]
  }
  const tags = (filters.tags ?? []).filter((tag) => tag.key !== '')
  if (tags.length) filters.tags = tags
  else delete filters.tags
  return { ...rule, filters }
}

/** Switching to a metric or monitor trigger drops what only an issue can use. */
function changeTrigger(rule: RuleDoc, trigger: RuleDoc['trigger']): RuleDoc {
  const next: RuleDoc = { ...rule, trigger }
  if (trigger.type === rule.trigger.type) return next
  if (isIncidental(trigger.type)) {
    next.actions = rule.actions.filter((a) => a.type === 'channel' || a.type === 'webhook')
    if (next.actions.length === 0) next.actions = [{ type: 'channel', channel_ids: [] }]
    next.filters = { ...rule.filters, tags: undefined, owner: undefined, kinds: undefined }
    next.notify_on_resolve = true
    delete next.frequency_minutes
  } else {
    delete next.notify_on_resolve
    if (trigger.type === 'event') next.frequency_minutes = rule.frequency_minutes ?? 60
    else delete next.frequency_minutes
  }
  return next
}

export function RuleBuilder({ alert, channels, assignees, monitorSlugs, orgSlug, error, pending, onClose, onSave }: {
  /** undefined: closed; null: a new rule. */
  alert: AlertRule | null | undefined
  channels: Channel[]
  assignees: { user_id: number; name: string }[]
  monitorSlugs: string[]
  orgSlug?: string
  error: Error | null
  pending: boolean
  onClose: () => void
  onSave: (input: AlertInput) => Promise<void>
}) {
  const [name, setName] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [rule, setRule] = useState<RuleDoc>(blankRule)
  useEffect(() => {
    setName(alert?.name ?? '')
    setEnabled(alert?.enabled ?? true)
    setRule(alert?.rule ?? blankRule())
  }, [alert])
  if (alert === undefined) return null

  const type: TriggerType = rule.trigger.type
  const incidental = isIncidental(type)
  const message = error?.message
  const parsed = parseFieldError(message)
  const unhandled = message && !(parsed && handledPaths.some((p) => parsed.path === p || parsed.path.startsWith(`${p}.`) || parsed.path.startsWith(`${p}[`)))

  function submit(event: FormEvent) {
    event.preventDefault()
    void onSave({ kind: 'app_rule', name: name.trim(), enabled, rule: cleanRule(rule), channel_ids: [] }).catch(() => undefined)
  }
  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose() }}>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <form className="flex flex-col gap-5" onSubmit={submit}>
          <DialogHeader>
            <DialogTitle>{alert ? 'Edit alert rule' : 'Create alert rule'}</DialogTitle>
            <DialogDescription>Pick a trigger, narrow it with filters, then choose what happens.</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field data-invalid={errorAt(message, 'name') ? true : undefined}>
              <FieldLabel htmlFor="rule-name">Name</FieldLabel>
              <Input id="rule-name" required maxLength={120} value={name} onChange={(e) => setName(e.target.value)} />
              {errorAt(message, 'name') ? <FieldError>{errorAt(message, 'name')}</FieldError> : null}
            </Field>
            <TriggerFields trigger={rule.trigger} error={message} monitorSlugs={monitorSlugs} onChange={(trigger) => setRule((r) => changeTrigger(r, trigger))} />
            {type === 'event' ? (
              <NumberField id="rule-frequency" label="At most one notice per issue every (minutes)" min={0} value={rule.frequency_minutes} error={errorAt(message, 'frequency_minutes')}
                onChange={(frequency_minutes) => setRule((r) => ({ ...r, frequency_minutes }))} />
            ) : null}
            {incidental ? (
              <CheckField id="rule-notify-resolve" label="Also notify when it resolves" checked={rule.notify_on_resolve ?? true} onChange={(checked) => setRule((r) => ({ ...r, notify_on_resolve: checked }))} />
            ) : null}
            <FilterFields filters={rule.filters} incidental={incidental} error={message} assignees={assignees} onChange={(filters) => setRule((r) => ({ ...r, filters }))} />
            <ActionFields actions={rule.actions} incidental={incidental} error={message} channels={channels} assignees={assignees} orgSlug={orgSlug} onChange={(actions) => setRule((r) => ({ ...r, actions }))} />
            <CheckField id="rule-enabled" label="Enabled" checked={enabled} onChange={setEnabled} />
          </FieldGroup>
          {unhandled ? <FieldError>{message}</FieldError> : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>Cancel</Button>
            <Button type="submit" disabled={pending}>Save rule</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
