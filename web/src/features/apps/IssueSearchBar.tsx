import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ApiProblem } from '@/lib/apiProblem'

import { useSavedViewMutations, useSavedViews } from './api'

const SYNTAX: [string, string][] = [
  ['is:unresolved', 'is: unresolved, resolved, ignored, archived, snoozed, assigned, unassigned'],
  ['assigned:me', 'assigned: me, none, or a member email'],
  ['release:1.4.0', 'release:, environment:, platform:, level:'],
  ['kind:crash', 'kind: and priority: (high, medium, low)'],
  ['first-seen:-24h', 'first-seen: and last-seen: with -24h, -7d, +30d or 2026-11-01'],
  ['events:>10', 'events: and users: with >N, <N, >=N, <=N'],
  ['!is:ignored', 'a leading ! negates a term'],
  ['"null pointer"', 'bare words match the title or culprit; quote values with spaces'],
]

/** Marks the character a syntax error points at. */
export function ErrorPointer({ text, position, message }: { text: string; position: number; message: string }) {
  const chars = Array.from(text)
  const at = Math.min(Math.max(position, 0), chars.length)
  return (
    <div role="alert" className="text-sm text-destructive">
      <p>{message}</p>
      <p aria-hidden="true" className="font-mono text-xs">
        {chars.slice(0, at).join('')}<mark className="rounded bg-destructive/20 px-0.5 text-destructive">{chars[at] ?? '␣'}</mark>{chars.slice(at + 1).join('')}
      </p>
    </div>
  )
}

/** The query bar: the search syntax, its inline error, a help list and the saved views menu. */
export function IssueSearchBar({ siteId, value, error, canEdit, onSubmit }: {
  siteId: number; value: string; error: Error | null; canEdit: boolean; onSubmit: (query: string) => void
}) {
  const [text, setText] = useState(value)
  const [help, setHelp] = useState(false)
  const [saving, setSaving] = useState(false)
  const [name, setName] = useState('')
  const [shared, setShared] = useState(false)
  const views = useSavedViews(siteId)
  const { create, remove } = useSavedViewMutations(siteId)
  const list = views.data?.views ?? []
  const current = list.find((view) => view.query === value && value !== '')
  const problem = error instanceof ApiProblem && error.position !== undefined ? error : null
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <form role="search" className="flex flex-1 gap-2" onSubmit={(event) => { event.preventDefault(); onSubmit(text.trim()) }}>
          <Input aria-label="Search query" aria-invalid={problem ? true : undefined} maxLength={200} className="min-w-64 flex-1 font-mono" placeholder="is:unresolved level:fatal release:1.4.0" value={text} onChange={(event) => setText(event.target.value)} />
          <Button type="submit" variant="secondary">Search</Button>
        </form>
        <Button type="button" variant="ghost" size="sm" aria-expanded={help} onClick={() => setHelp(!help)}>Syntax help</Button>
        <label className="flex items-center gap-2 text-sm">
          <span className="text-muted-foreground">Views</span>
          <select className="h-9 rounded-lg border border-input bg-background px-2 text-sm" aria-label="Saved views" value={current?.id ?? ''}
            onChange={(event) => { const view = list.find((item) => item.id === Number(event.target.value)); if (view) { setText(view.query); onSubmit(view.query) } }}>
            <option value="">{list.length === 0 ? 'No saved views' : 'Choose a view…'}</option>
            {list.map((view) => <option key={view.id} value={view.id}>{view.name}{view.shared ? ' (shared)' : ''}</option>)}
          </select>
        </label>
        {current?.mine && canEdit ? (
          <Button type="button" variant="ghost" size="sm" disabled={remove.isPending} onClick={() => remove.mutate(current.id)}>Delete view</Button>
        ) : null}
        {canEdit && !saving ? <Button type="button" variant="outline" size="sm" onClick={() => setSaving(true)}>Save view</Button> : null}
      </div>
      {saving ? (
        <form className="flex flex-wrap items-center gap-2" onSubmit={(event) => {
          event.preventDefault()
          create.mutate({ name: name.trim(), query: text.trim(), shared }, { onSuccess: () => { setSaving(false); setName(''); setShared(false) } })
        }}>
          <Input aria-label="View name" className="w-56" maxLength={80} placeholder="Name this view" value={name} onChange={(event) => setName(event.target.value)} />
          <label className="flex items-center gap-1 text-sm"><input type="checkbox" checked={shared} onChange={(event) => setShared(event.target.checked)} /> Share with the project</label>
          <Button type="submit" size="sm" disabled={name.trim() === '' || create.isPending}>Save</Button>
          <Button type="button" variant="ghost" size="sm" onClick={() => setSaving(false)}>Cancel</Button>
          {create.isError ? <span role="alert" className="text-sm text-destructive">{create.error.message}</span> : null}
        </form>
      ) : null}
      {problem ? <ErrorPointer text={value} position={problem.position!} message={problem.message} /> : null}
      {error && !problem ? <p role="alert" className="text-sm text-destructive">{error.message}</p> : null}
      {help ? (
        <dl className="grid gap-x-4 gap-y-1 rounded-lg border p-3 text-sm sm:grid-cols-[max-content_1fr]" aria-label="Search syntax">
          {SYNTAX.map(([example, hint]) => (
            <div key={example} className="contents"><dt className="font-mono text-xs">{example}</dt><dd className="text-muted-foreground">{hint}</dd></div>
          ))}
        </dl>
      ) : null}
    </div>
  )
}
