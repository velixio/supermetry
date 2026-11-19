import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useAppAssignees } from '@/features/apps/api'
import { privateRequest } from '@/features/settings/privateApi'

type OwnershipRule = { matcher: string; value: string; owner_user_id: number }
type GroupingRule = { matcher: string; value: string; fingerprint: string }
type Rules = { ownership: OwnershipRule[]; grouping: GroupingRule[] }

const selectClass = 'h-9 rounded-lg border border-input bg-background px-2 text-sm'
const OWNERSHIP_MATCHERS = ['path', 'module', 'tag']
const GROUPING_MATCHERS = ['error_type', 'message', 'path', 'module', 'tag']
const MAX_RULES = 200

// A matcher is stored as "path", "module" or "tag:<key>"; the editor splits the tag key out.
function split(matcher: string): [string, string] {
  return matcher.startsWith('tag:') ? ['tag', matcher.slice(4)] : [matcher, '']
}
const join = (kind: string, key: string) => (kind === 'tag' ? `tag:${key}` : kind)

/** Ownership and grouping rules of an app project. Owner, admin and editor can change them. */
export function AppRulesSection({ orgId, siteId, canEdit }: { orgId: number; siteId: number; canEdit: boolean }) {
  const client = useQueryClient()
  const path = `/api/private/sites/${siteId}/app/rules`
  const rules = useQuery({ queryKey: ['app-rules', siteId], queryFn: () => privateRequest<Rules>('GET', path, { orgId }) })
  const members = useAppAssignees(siteId).data?.members ?? []
  const [ownership, setOwnership] = useState<OwnershipRule[]>([])
  const [grouping, setGrouping] = useState<GroupingRule[]>([])
  useEffect(() => {
    if (rules.data) { setOwnership(rules.data.ownership ?? []); setGrouping(rules.data.grouping ?? []) }
  }, [rules.data])

  const save = useMutation({
    mutationFn: () => privateRequest<Rules>('PUT', path, { orgId, body: { ownership, grouping } }),
    onSuccess: (value) => client.setQueryData(['app-rules', siteId], value),
  })
  const patchOwn = (i: number, patch: Partial<OwnershipRule>) => setOwnership(ownership.map((r, k) => (k === i ? { ...r, ...patch } : r)))
  const patchGrp = (i: number, patch: Partial<GroupingRule>) => setGrouping(grouping.map((r, k) => (k === i ? { ...r, ...patch } : r)))
  const move = <T,>(list: T[], i: number, by: number): T[] => {
    const j = i + by
    if (j < 0 || j >= list.length) return list
    const next = [...list]
    ;[next[i], next[j]] = [next[j], next[i]]
    return next
  }

  return (
    <section className="flex flex-col gap-6 rounded-lg bg-card p-4" aria-label="Issue rules">
      <div className="flex flex-col gap-3">
        <div>
          <h3 className="text-sm font-semibold">Ownership</h3>
          <p className="text-sm text-muted-foreground">
            Assign a new issue to a member when an in-app frame or a tag matches. The first matching rule wins; existing issues are never re-assigned.
            Values are globs: <code>*</code> any run, <code>?</code> one character.
          </p>
        </div>
        {ownership.map((rule, i) => {
          const [kind, key] = split(rule.matcher)
          return (
            <div key={i} className="flex flex-wrap items-center gap-2" role="group" aria-label={`Ownership rule ${i + 1}`}>
              <select className={selectClass} aria-label="Matcher" disabled={!canEdit} value={kind} onChange={(e) => patchOwn(i, { matcher: join(e.target.value, key) })}>
                {OWNERSHIP_MATCHERS.map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
              {kind === 'tag' ? <Input className="w-28" aria-label="Tag key" disabled={!canEdit} value={key} onChange={(e) => patchOwn(i, { matcher: join('tag', e.target.value) })} /> : null}
              <Input className="w-64" aria-label="Pattern" disabled={!canEdit} value={rule.value} onChange={(e) => patchOwn(i, { value: e.target.value })} />
              <select className={selectClass} aria-label="Owner" disabled={!canEdit} value={rule.owner_user_id} onChange={(e) => patchOwn(i, { owner_user_id: Number(e.target.value) })}>
                <option value={0}>Choose owner</option>
                {members.map((m) => <option key={m.user_id} value={m.user_id}>{m.name}</option>)}
              </select>
              {canEdit ? (
                <>
                  <Button type="button" variant="outline" size="sm" aria-label="Move ownership rule up" onClick={() => setOwnership(move(ownership, i, -1))}>Up</Button>
                  <Button type="button" variant="outline" size="sm" aria-label="Move ownership rule down" onClick={() => setOwnership(move(ownership, i, 1))}>Down</Button>
                  <Button type="button" variant="outline" size="sm" onClick={() => setOwnership(ownership.filter((_, k) => k !== i))}>Remove ownership rule</Button>
                </>
              ) : null}
            </div>
          )
        })}
        {canEdit ? (
          <div>
            <Button type="button" variant="outline" size="sm" disabled={ownership.length >= MAX_RULES}
              onClick={() => setOwnership([...ownership, { matcher: 'path', value: '', owner_user_id: 0 }])}>Add ownership rule</Button>
          </div>
        ) : null}
      </div>

      <div className="flex flex-col gap-3">
        <div>
          <h3 className="text-sm font-semibold">Grouping</h3>
          <p className="text-sm text-muted-foreground">
            Force a fingerprint for matching events, so different stacks land in one issue. Use <code>{'{{ default }}'}</code> in the fingerprint to keep the
            default grouping and append to it. Applies to new events only.
          </p>
        </div>
        {grouping.map((rule, i) => {
          const [kind, key] = split(rule.matcher)
          return (
            <div key={i} className="flex flex-wrap items-center gap-2" role="group" aria-label={`Grouping rule ${i + 1}`}>
              <select className={selectClass} aria-label="Matcher" disabled={!canEdit} value={kind} onChange={(e) => patchGrp(i, { matcher: join(e.target.value, key) })}>
                {GROUPING_MATCHERS.map((m) => <option key={m} value={m}>{m}</option>)}
              </select>
              {kind === 'tag' ? <Input className="w-28" aria-label="Tag key" disabled={!canEdit} value={key} onChange={(e) => patchGrp(i, { matcher: join('tag', e.target.value) })} /> : null}
              <Input className="w-64" aria-label="Pattern" disabled={!canEdit} value={rule.value} onChange={(e) => patchGrp(i, { value: e.target.value })} />
              <Input className="w-48" aria-label="Fingerprint" maxLength={64} disabled={!canEdit} value={rule.fingerprint} onChange={(e) => patchGrp(i, { fingerprint: e.target.value })} />
              {canEdit ? (
                <>
                  <Button type="button" variant="outline" size="sm" aria-label="Move grouping rule up" onClick={() => setGrouping(move(grouping, i, -1))}>Up</Button>
                  <Button type="button" variant="outline" size="sm" aria-label="Move grouping rule down" onClick={() => setGrouping(move(grouping, i, 1))}>Down</Button>
                  <Button type="button" variant="outline" size="sm" onClick={() => setGrouping(grouping.filter((_, k) => k !== i))}>Remove grouping rule</Button>
                </>
              ) : null}
            </div>
          )
        })}
        {canEdit ? (
          <div>
            <Button type="button" variant="outline" size="sm" disabled={grouping.length >= MAX_RULES}
              onClick={() => setGrouping([...grouping, { matcher: 'error_type', value: '', fingerprint: '' }])}>Add grouping rule</Button>
          </div>
        ) : null}
      </div>

      {canEdit ? (
        <div className="flex items-center gap-3">
          <Button type="button" disabled={save.isPending} onClick={() => save.mutate()}>Save rules</Button>
          {save.isError ? <span role="alert" className="text-sm text-destructive">{save.error instanceof Error ? save.error.message : 'Could not save the rules'}</span> : null}
          {save.isSuccess ? <span className="text-sm text-muted-foreground">Saved</span> : null}
        </div>
      ) : null}
    </section>
  )
}
