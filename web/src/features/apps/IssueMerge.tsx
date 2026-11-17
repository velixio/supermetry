import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { fmt } from '@/lib/format'

import { useMergedIssues, useMergeMutations } from './api'

/** Parses "12, 15 16" into issue ids; null when any part is not a positive whole number. */
export function parseIssueIds(text: string): number[] | null {
  const parts = text.split(/[\s,]+/).filter(Boolean)
  if (parts.length === 0) return null
  const ids = parts.map((part) => (/^[1-9][0-9]{0,15}$/.test(part) ? Number(part) : NaN))
  return ids.some((id) => !Number.isSafeInteger(id)) ? null : ids
}

/** Merge other issues into this one, and the list of merged issues with an unmerge button per fingerprint hash. */
export function IssueMerge({ siteId, issueId, canEdit }: { siteId: number; issueId: number; canEdit: boolean }) {
  const merged = useMergedIssues(siteId, issueId)
  const { merge, unmerge } = useMergeMutations(siteId, issueId)
  const [draft, setDraft] = useState('')
  const ids = parseIssueIds(draft)
  const rows = merged.data?.merged ?? []
  const error = merge.error ?? unmerge.error ?? (merged.isError ? merged.error : null)
  if (!canEdit && rows.length === 0 && !error) return null
  return (
    <Card>
      <CardHeader>
        <CardTitle>Merged issues</CardTitle>
        <CardDescription>Events of merged issues count here, and new events with their fingerprints land on this issue.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {merged.isSuccess && rows.length === 0 ? <p className="text-sm text-muted-foreground">No issues are merged into this one.</p> : null}
        <ul className="flex flex-col gap-2">
          {rows.map((row) => (
            <li key={row.hash} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border p-3 text-sm">
              <span className="min-w-0">
                <span className="block truncate font-medium">{row.title}</span>
                <span className="text-xs text-muted-foreground">Issue {row.issue_id} · {fmt.number(row.events)} events here · <code title={row.hash}>{row.hash.slice(0, 12)}</code></span>
              </span>
              {canEdit ? <Button type="button" size="sm" variant="outline" disabled={unmerge.isPending} aria-label={`Unmerge ${row.title}`} onClick={() => unmerge.mutate(row.hash)}>Unmerge</Button> : null}
            </li>
          ))}
        </ul>
        {error ? <p role="alert" className="text-sm text-destructive">{error.message}</p> : null}
        {canEdit ? (
          <form className="flex flex-wrap items-end gap-2" onSubmit={(event) => { event.preventDefault(); if (ids) merge.mutate(ids, { onSuccess: () => setDraft('') }) }}>
            <label className="flex flex-col gap-1 text-sm">
              <span className="text-muted-foreground">Merge issues into this one (ids, comma separated)</span>
              <Input aria-label="Issue ids to merge in" value={draft} inputMode="numeric" onChange={(event) => setDraft(event.target.value)} placeholder="12, 15" />
            </label>
            <Button type="submit" disabled={merge.isPending || ids === null}>Merge</Button>
          </form>
        ) : null}
      </CardContent>
    </Card>
  )
}
