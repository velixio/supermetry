import { PlusIcon, Trash2Icon } from 'lucide-react'
import { useId } from 'react'

import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'

import { blankCondition, type DraftCohort, type DraftCondition } from './conditions'
import { maxConditions, maxWindowDays, type ConditionType } from './types'

const t = en.cohorts

const selectClass = 'h-11 w-full rounded-md border bg-background px-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring md:h-9'
const inputClass = 'h-11 md:h-9'
const conditionTypes: ConditionType[] = ['event', 'page', 'event_property', 'activity', 'first_seen', 'last_seen']

function ConditionFields({ condition, onChange }: { condition: DraftCondition; onChange: (next: DraftCondition) => void }) {
  const id = useId()
  const set = <K extends keyof DraftCondition>(field: K, value: DraftCondition[K]) => onChange({ ...condition, [field]: value })
  const minCount = (
    <Field>
      <FieldLabel htmlFor={`${id}-count`}>{t.fields.minCount}</FieldLabel>
      <Input className={inputClass} id={`${id}-count`} inputMode="numeric" value={condition.minCount} onChange={(event) => set('minCount', event.target.value)} />
    </Field>
  )
  switch (condition.type) {
    case 'event':
      return <>
        <Field>
          <FieldLabel htmlFor={`${id}-name`}>{t.fields.eventName}</FieldLabel>
          <Input autoComplete="off" className={inputClass} id={`${id}-name`} value={condition.name} onChange={(event) => set('name', event.target.value)} />
        </Field>
        {minCount}
      </>
    case 'event_property':
      return <>
        <Field>
          <FieldLabel htmlFor={`${id}-name`}>{t.fields.eventName}</FieldLabel>
          <Input autoComplete="off" className={inputClass} id={`${id}-name`} value={condition.name} onChange={(event) => set('name', event.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-key`}>{t.fields.propertyKey}</FieldLabel>
          <Input autoComplete="off" className={inputClass} id={`${id}-key`} value={condition.propKey} onChange={(event) => set('propKey', event.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-value`}>{t.fields.propertyValue}</FieldLabel>
          <Input autoComplete="off" className={inputClass} id={`${id}-value`} value={condition.propValue} onChange={(event) => set('propValue', event.target.value)} />
        </Field>
        {minCount}
      </>
    case 'page':
      return <>
        <Field>
          <FieldLabel htmlFor={`${id}-operator`}>{t.fields.pageOperator}</FieldLabel>
          <select className={selectClass} id={`${id}-operator`} value={condition.operator} onChange={(event) => set('operator', event.target.value as DraftCondition['operator'])}>
            <option value="is">{t.fields.pageIs}</option>
            <option value="starts_with">{t.fields.pageStartsWith}</option>
          </select>
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-path`}>{t.fields.pagePath}</FieldLabel>
          <Input autoComplete="off" className={inputClass} id={`${id}-path`} placeholder="/pricing" value={condition.path} onChange={(event) => set('path', event.target.value)} />
        </Field>
        {minCount}
      </>
    case 'activity':
      return <>
        <Field>
          <FieldLabel htmlFor={`${id}-metric`}>{t.fields.metric}</FieldLabel>
          <select className={selectClass} id={`${id}-metric`} value={condition.metric} onChange={(event) => set('metric', event.target.value as DraftCondition['metric'])}>
            <option value="sessions">{t.metricSessions}</option>
            <option value="events">{t.metricEvents}</option>
          </select>
        </Field>
        {minCount}
      </>
    default:
      return <>
        <Field>
          <FieldLabel htmlFor={`${id}-relation`}>{t.fields.relation}</FieldLabel>
          <select className={selectClass} id={`${id}-relation`} value={condition.relation} onChange={(event) => set('relation', event.target.value as DraftCondition['relation'])}>
            <option value="within">{`${t.within} the last…`}</option>
            <option value="before">{`${t.before} the last…`}</option>
          </select>
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-days`}>{t.fields.days}</FieldLabel>
          <Input className={inputClass} id={`${id}-days`} inputMode="numeric" value={condition.days} onChange={(event) => set('days', event.target.value)} />
        </Field>
      </>
  }
}

function ConditionRow({
  condition, position, onChange, onRemove, removable,
}: {
  condition: DraftCondition
  position: number
  onChange: (next: DraftCondition) => void
  onRemove: () => void
  removable: boolean
}) {
  const id = useId()
  return (
    <fieldset className="flex min-w-0 flex-col gap-3 rounded-lg border bg-card p-3">
      <legend className="px-1 text-sm font-medium">{t.conditionLabel(position)}</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor={`${id}-type`}>{t.conditionType}</FieldLabel>
          <select
            className={selectClass}
            id={`${id}-type`}
            value={condition.type}
            onChange={(event) => onChange({ ...blankCondition(event.target.value as ConditionType), key: condition.key, negate: condition.negate })}
          >
            {conditionTypes.map((type) => <option key={type} value={type}>{t.types[type]}</option>)}
          </select>
        </Field>
        <ConditionFields condition={condition} onChange={onChange} />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="flex min-h-11 items-center gap-2 text-sm md:min-h-9">
          <input checked={condition.negate} className="size-4" type="checkbox" onChange={(event) => onChange({ ...condition, negate: event.target.checked })} />
          {t.negateLabel}
        </label>
        <Button aria-label={t.removeCondition(position)} className="h-11 md:h-8" disabled={!removable} type="button" variant="outline" onClick={onRemove}>
          <Trash2Icon data-icon="inline-start" />
          Remove
        </Button>
      </div>
    </fieldset>
  )
}

export function ConditionBuilder({ draft, onChange }: { draft: DraftCohort; onChange: (next: DraftCohort) => void }) {
  const id = useId()
  const update = (index: number, next: DraftCondition) =>
    onChange({ ...draft, conditions: draft.conditions.map((condition, position) => position === index ? next : condition) })
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)_minmax(0,1fr)]">
        <Field>
          <FieldLabel htmlFor={`${id}-name`}>{t.name}</FieldLabel>
          <Input className={inputClass} id={`${id}-name`} maxLength={80} value={draft.name} onChange={(event) => onChange({ ...draft, name: event.target.value })} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-match`}>{t.match}</FieldLabel>
          <select className={selectClass} id={`${id}-match`} value={draft.match} onChange={(event) => onChange({ ...draft, match: event.target.value as DraftCohort['match'] })}>
            <option value="all">{t.matchAll}</option>
            <option value="any">{t.matchAny}</option>
          </select>
        </Field>
        <Field>
          <FieldLabel htmlFor={`${id}-window`}>{t.window}</FieldLabel>
          <Input aria-describedby={`${id}-window-help`} className={inputClass} id={`${id}-window`} inputMode="numeric" max={maxWindowDays} value={draft.windowDays} onChange={(event) => onChange({ ...draft, windowDays: event.target.value })} />
        </Field>
      </div>
      <p className="-mt-2 text-xs text-muted-foreground" id={`${id}-window-help`}>{t.windowHelp}</p>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium">{t.conditions}</h3>
        {draft.conditions.map((condition, index) => (
          <ConditionRow
            key={condition.key}
            condition={condition}
            position={index + 1}
            removable={draft.conditions.length > 1}
            onChange={(next) => update(index, next)}
            onRemove={() => onChange({ ...draft, conditions: draft.conditions.filter((_, position) => position !== index) })}
          />
        ))}
        <div>
          <Button className="h-11 md:h-8" disabled={draft.conditions.length >= maxConditions} type="button" variant="outline" onClick={() => onChange({ ...draft, conditions: [...draft.conditions, blankCondition()] })}>
            <PlusIcon data-icon="inline-start" />
            {t.addCondition}
          </Button>
        </div>
      </div>
    </div>
  )
}
