import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

import { useAppAssignees, useIssueActivity, type AppIssueActivity } from './api'

type Data = Record<string, unknown>

const text = (value: unknown) => (typeof value === 'string' || typeof value === 'number' ? String(value) : '')

/** One sentence per activity kind; details come from the row's data. */
export function describeActivity(a: AppIssueActivity, nameOf: (id: unknown) => string): string {
  const d = (a.data ?? {}) as Data
  switch (a.kind) {
    case 'status': {
      const release = d.resolved_next ? ' in the next release' : d.resolved_in_release ? ` in ${text(d.resolved_in_release)}` : ''
      return `changed the status from ${text(d.from)} to ${text(d.to)}${release}`
    }
    case 'assigned': return `assigned the issue to ${nameOf(d.assignee)}`
    case 'unassigned': return 'removed the assignee'
    case 'priority': return `changed the priority from ${text(d.from)} to ${text(d.to)}`
    case 'snoozed': {
      const limits = [d.until ? `until ${text(d.until).slice(0, 16).replace('T', ' ')}` : '', d.events ? `for ${text(d.events)} more events` : '', d.users ? `for ${text(d.users)} more users` : ''].filter(Boolean)
      return `snoozed the issue ${limits.join(' or ')}`
    }
    case 'archived': return d.archived === false ? 'unarchived the issue' : 'archived the issue until it escalates'
    case 'escalated': return 'reopened the archived issue because it escalated'
    case 'commented': return 'commented'
    case 'merged': return 'merged an issue into this one'
    case 'unmerged': return 'unmerged an issue'
    case 'github_linked': return d.url ? `linked ${text(d.url)}` : 'removed the GitHub link'
    case 'regressed': return `regressed${d.release ? ` in ${text(d.release)}` : ''}`
    case 'first_seen': return `first seen${d.release ? ` in ${text(d.release)}` : ''}`
    default: return a.kind
  }
}

export function IssueActivity({ siteId, issueId }: { siteId: number; issueId: number }) {
  const activity = useIssueActivity(siteId, issueId)
  const members = useAppAssignees(siteId)
  const nameOf = (id: unknown) => members.data?.members.find((m) => m.user_id === id)?.name || 'a teammate'
  const rows = activity.data?.pages.flatMap((page) => page.activity) ?? []
  return (
    <Card>
      <CardHeader><CardTitle>Activity</CardTitle><CardDescription>Every change to this issue, newest first.</CardDescription></CardHeader>
      <CardContent>
        {activity.isPending ? <p role="status" className="text-sm text-muted-foreground">Loading activity…</p> : null}
        {activity.isError ? <p role="alert" className="text-sm text-destructive">{activity.error.message}</p> : null}
        {activity.isSuccess && rows.length === 0 ? <p className="text-sm text-muted-foreground">No activity yet.</p> : null}
        <ol aria-label="Activity timeline" className="flex flex-col gap-2">
          {rows.map((a) => (
            <li key={a.id} className="flex flex-wrap items-baseline gap-x-2 text-sm">
              <time dateTime={a.at} className="tabular-nums text-xs text-muted-foreground">{a.at.slice(0, 16).replace('T', ' ')}</time>
              <span><strong className="font-medium">{a.actor_name || 'System'}</strong> {describeActivity(a, nameOf)}</span>
            </li>
          ))}
        </ol>
        {activity.hasNextPage ? <div className="mt-3"><Button type="button" size="sm" variant="outline" disabled={activity.isFetchingNextPage} onClick={() => void activity.fetchNextPage()}>Load older activity</Button></div> : null}
      </CardContent>
    </Card>
  )
}
