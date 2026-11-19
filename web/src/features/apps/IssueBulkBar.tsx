import { Button } from '@/components/ui/button'

import { useAppAssignees, useBulkIssues, type AppIssueChange, type BulkTarget } from './api'

const selectClass = 'h-9 rounded-lg border border-input bg-background px-2 text-sm disabled:opacity-50'

/** Actions on the checked issues, or on every issue the current search matches. */
export function IssueBulkBar({ siteId, count, matching, target, onDone }: {
  siteId: number
  /** How many issues the action will cover, for the label. */
  count: number
  /** Whether the action covers everything the search matches rather than the checked rows. */
  matching: boolean
  target: BulkTarget
  /** Called with a summary such as "Updated 3, skipped 1." once the change went through. */
  onDone: (summary: string) => void
}) {
  const bulk = useBulkIssues(siteId)
  const assignees = useAppAssignees(siteId)
  const run = (change: AppIssueChange) => {
    bulk.mutate({ target, change }, {
      onSuccess: (result) => {
        onDone(`Updated ${result.updated.length}${result.skipped.length > 0 ? `, skipped ${result.skipped.length}` : ''}.`)
      },
    })
  }
  return (
    <div role="toolbar" aria-label="Bulk actions" className="flex flex-wrap items-center gap-2 rounded-lg bg-muted/40 p-2 text-sm">
      <span className="font-medium">{matching ? `All ${count} matching issues` : `${count} selected`}</span>
      <Button size="sm" variant="outline" disabled={bulk.isPending} onClick={() => run({ status: 'resolved' })}>Resolve</Button>
      <Button size="sm" variant="outline" disabled={bulk.isPending} onClick={() => run({ status: 'ignored' })}>Ignore</Button>
      <Button size="sm" variant="outline" disabled={bulk.isPending} onClick={() => run({ archive: true })}>Archive</Button>
      <select className={selectClass} aria-label="Assign selected to" value="" disabled={bulk.isPending}
        onChange={(event) => { if (event.target.value !== '') run({ assignee: Number(event.target.value) }) }}>
        <option value="">Assign to…</option>
        <option value={0}>Unassigned</option>
        {(assignees.data?.members ?? []).map((member) => <option key={member.user_id} value={member.user_id}>{member.name}</option>)}
      </select>
      <select className={selectClass} aria-label="Set priority of selected" value="" disabled={bulk.isPending}
        onChange={(event) => { if (event.target.value !== '') run({ priority: event.target.value as 'high' | 'medium' | 'low' }) }}>
        <option value="">Priority…</option>
        <option value="high">High</option><option value="medium">Medium</option><option value="low">Low</option>
      </select>
      {bulk.isError ? <span role="alert" className="text-destructive">{bulk.error.message}</span> : null}
    </div>
  )
}
