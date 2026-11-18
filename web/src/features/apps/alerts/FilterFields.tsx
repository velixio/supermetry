import { PlusIcon, XIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel, FieldLegend, FieldSet } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import { CheckField, selectClass, splitList } from './fieldBits'
import { errorAt, type RuleFilters } from './ruleTypes'

const issueKinds = ['crash', 'error', 'hang', 'install_failed', 'install_stalled', 'incompatibility', 'operation_failed']
const lists = [
  ['environments', 'Environments'], ['releases', 'Releases'], ['platforms', 'Platforms'], ['levels', 'Levels'],
] as const

/** Filters narrow what the rule looks at. Tag and owner filters need an issue, so metric and monitor rules hide them. */
export function FilterFields({ filters, onChange, incidental, error, assignees }: {
  filters: RuleFilters
  onChange: (filters: RuleFilters) => void
  incidental: boolean
  error?: string
  assignees: { user_id: number; name: string }[]
}) {
  const tags = filters.tags ?? []
  const set = (patch: Partial<RuleFilters>) => onChange({ ...filters, ...patch })
  return (
    <FieldSet>
      <FieldLegend>Filters</FieldLegend>
      <div className="grid gap-3 sm:grid-cols-2">
        {lists.map(([key, label]) => (
          <Field key={key}>
            <FieldLabel htmlFor={`rule-filter-${key}`}>{label} (comma separated)</FieldLabel>
            <Input id={`rule-filter-${key}`} value={(filters[key] ?? []).join(', ')} onChange={(e) => set({ [key]: splitList(e.target.value) })} />
            {errorAt(error, `filters.${key}`) ? <FieldError>{errorAt(error, `filters.${key}`)}</FieldError> : null}
          </Field>
        ))}
      </div>
      {!incidental ? (
        <>
          <FieldSet>
            <FieldLegend variant="label">Issue kinds (none selected means every kind)</FieldLegend>
            {issueKinds.map((kind) => (
              <CheckField key={kind} id={`rule-kind-${kind}`} label={kind} checked={filters.kinds?.includes(kind) ?? false}
                onChange={(checked) => set({ kinds: checked ? [...(filters.kinds ?? []), kind] : (filters.kinds ?? []).filter((k) => k !== kind) })} />
            ))}
            {errorAt(error, 'filters.kinds') ? <FieldError>{errorAt(error, 'filters.kinds')}</FieldError> : null}
          </FieldSet>
          <Field>
            <FieldLabel htmlFor="rule-filter-owner">Owner</FieldLabel>
            <select id="rule-filter-owner" className={selectClass} value={filters.owner ? String(filters.owner.user_id) : ''}
              onChange={(e) => set({ owner: e.target.value === '' ? undefined : { user_id: Number(e.target.value) } })}>
              <option value="">Any owner</option><option value="0">Unassigned</option>
              {assignees.map((member) => <option key={member.user_id} value={member.user_id}>{member.name}</option>)}
            </select>
            {errorAt(error, 'filters.owner') ? <FieldError>{errorAt(error, 'filters.owner')}</FieldError> : null}
          </Field>
          <FieldSet>
            <FieldLegend variant="label">Tags</FieldLegend>
            {tags.map((tag, index) => (
              <div className="flex flex-wrap items-center gap-2" key={index}>
                <Input aria-label={`Tag ${index + 1} key`} className="w-36" placeholder="key" value={tag.key} onChange={(e) => set({ tags: tags.map((t, i) => (i === index ? { ...t, key: e.target.value } : t)) })} />
                <select aria-label={`Tag ${index + 1} match`} className={selectClass} value={tag.negate ? 'not' : 'is'} onChange={(e) => set({ tags: tags.map((t, i) => (i === index ? { ...t, negate: e.target.value === 'not' } : t)) })}>
                  <option value="is">is</option><option value="not">is not</option>
                </select>
                <Input aria-label={`Tag ${index + 1} value`} className="w-36" placeholder="value" value={tag.value} onChange={(e) => set({ tags: tags.map((t, i) => (i === index ? { ...t, value: e.target.value } : t)) })} />
                <Button type="button" variant="ghost" size="icon" aria-label={`Remove tag ${index + 1}`} onClick={() => set({ tags: tags.filter((_, i) => i !== index) })}><XIcon /></Button>
              </div>
            ))}
            {errorAt(error, 'filters.tags') ? <FieldError>{errorAt(error, 'filters.tags')}</FieldError> : null}
            <div><Button type="button" variant="outline" size="sm" onClick={() => set({ tags: [...tags, { key: '', value: '' }] })}><PlusIcon />Add tag filter</Button></div>
          </FieldSet>
        </>
      ) : null}
    </FieldSet>
  )
}
