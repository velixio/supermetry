import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

import { useAppAssignees, useUpdateIssue, type AppIssueDetailData } from './api'

const selectClass = 'h-9 rounded-lg border border-input bg-background px-2 text-sm disabled:opacity-50'

const HOUR = 3_600_000
const SNOOZES: Record<string, { label: string; change: (now: number) => { until?: string; events?: number; users?: number } }> = {
  '1h': { label: 'For 1 hour', change: (now) => ({ until: new Date(now + HOUR).toISOString() }) },
  '1d': { label: 'For 1 day', change: (now) => ({ until: new Date(now + 24 * HOUR).toISOString() }) },
  '1w': { label: 'For 1 week', change: (now) => ({ until: new Date(now + 168 * HOUR).toISOString() }) },
  e100: { label: 'Until 100 more events', change: () => ({ events: 100 }) },
  u10: { label: 'Until 10 more users', change: () => ({ users: 10 }) },
}

/** Assignee, priority and snooze controls. Viewers see the values but cannot change them. */
export function IssueWorkflow({ siteId, issue, canEdit }: { siteId: number; issue: AppIssueDetailData; canEdit: boolean }) {
  const update = useUpdateIssue(siteId, issue.id)
  const assignees = useAppAssignees(siteId)
  const members = assignees.data?.members ?? []
  const known = members.some((m) => m.user_id === issue.assignee_user_id)
  const closed = issue.status === 'archived'
  return (
    <Card>
      <CardHeader><CardTitle className="text-base">Workflow</CardTitle></CardHeader>
      <CardContent className="flex flex-wrap items-center gap-4 text-sm">
        <label className="flex items-center gap-2">
          <span className="text-muted-foreground">Assignee</span>
          <select className={selectClass} aria-label="Assignee" disabled={!canEdit || update.isPending} value={issue.assignee_user_id ?? 0}
            onChange={(event) => update.mutate({ assignee: Number(event.target.value) })}>
            <option value={0}>Unassigned</option>
            {issue.assignee_user_id && !known ? <option value={issue.assignee_user_id}>{issue.assignee_name || 'Former member'}</option> : null}
            {members.map((m) => <option key={m.user_id} value={m.user_id}>{m.name}</option>)}
          </select>
        </label>
        <label className="flex items-center gap-2">
          <span className="text-muted-foreground">Priority</span>
          <select className={selectClass} aria-label="Priority" disabled={!canEdit} value={issue.priority}
            onChange={(event) => update.mutate({ priority: event.target.value as 'high' | 'medium' | 'low' })}>
            <option value="high">High</option><option value="medium">Medium</option><option value="low">Low</option>
          </select>
        </label>
        {canEdit && !closed ? (
          <label className="flex items-center gap-2">
            <span className="text-muted-foreground">Snooze</span>
            <select className={selectClass} aria-label="Snooze" value="" disabled={update.isPending}
              onChange={(event) => { const preset = SNOOZES[event.target.value]; if (preset) update.mutate({ snooze: preset.change(Date.now()) }) }}>
              <option value="">Choose…</option>
              {Object.entries(SNOOZES).map(([key, preset]) => <option key={key} value={key}>{preset.label}</option>)}
            </select>
          </label>
        ) : null}
        {issue.snooze_until || issue.snooze_events || issue.snooze_users ? (
          <span className="text-muted-foreground">
            Snoozed{issue.snooze_until ? ` until ${issue.snooze_until.slice(0, 16).replace('T', ' ')}` : ''}
            {issue.snooze_events ? ` · for ${issue.snooze_events} more events` : ''}{issue.snooze_users ? ` · for ${issue.snooze_users} more users` : ''}
          </span>
        ) : null}
        {issue.github_url ? <a className="font-medium underline" href={issue.github_url} target="_blank" rel="noreferrer noopener">GitHub issue</a> : null}
        {update.isError ? <p role="alert" className="w-full text-sm text-destructive">{update.error.message}</p> : null}
      </CardContent>
    </Card>
  )
}
