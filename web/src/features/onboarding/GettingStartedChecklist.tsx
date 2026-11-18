import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { CheckCircle2Icon, CircleIcon, XIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { useCreateDemo } from '@/features/demo/useDemo'
import { useGoals } from '@/features/goals/api'
import { privateRequest, type ListResponse } from '@/features/settings/privateApi'
import type { ManagedSite as SiteDetails } from '@/features/sites/types'
import { uiOnboarding } from '@/messages/uiOnboarding'

import { checklistItems, checklistProgress, readDismissed, writeDismissed, type ChecklistId, type ChecklistKey } from './checklist'

type Props = {
  orgId: number
  orgSlug: string
  siteId: number
  siteSlug: string
  userId: number
  /** Only people who can add sites, invite members and create keys see the checklist. */
  canManage: boolean
  isDemo?: boolean
}

export function GettingStartedChecklist({ orgId, orgSlug, siteId, siteSlug, userId, canManage, isDemo = false }: Props) {
  const [dismissed, setDismissed] = useState(() => readDismissed(userId, siteId))
  const active = canManage && !isDemo && !dismissed
  const status = useQuery({
    queryKey: ['tracking-status', siteId],
    queryFn: () => privateRequest<SiteDetails>('GET', `/api/private/sites/${siteId}`, { orgId }),
    enabled: active,
  })
  const goals = useGoals(siteId)
  const members = useQuery({
    queryKey: ['members', orgId],
    queryFn: () => privateRequest<ListResponse<unknown>>('GET', `/api/private/orgs/${orgId}/members`, { orgId }),
    enabled: active,
  })
  const invitations = useQuery({
    queryKey: ['invitations', orgId],
    queryFn: () => privateRequest<ListResponse<unknown>>('GET', `/api/private/orgs/${orgId}/invitations`, { orgId }),
    enabled: active,
  })
  const keys = useQuery({
    queryKey: ['api-keys', orgId],
    queryFn: () => privateRequest<ListResponse<ChecklistKey>>('GET', `/api/private/orgs/${orgId}/api-keys`, { orgId }),
    enabled: active,
  })
  const demo = useCreateDemo({ id: orgId, slug: orgSlug })

  // Wait for every answer so items do not tick on one by one. A failed lookup counts as not done.
  const settled = [status, members, invitations, keys].every((query) => !query.isPending)
  if (!active || !settled || goals.isPending) return null

  const items = checklistItems({
    eventReceived: status.data?.install_status !== undefined && status.data.install_status !== 'waiting',
    goalCount: Array.isArray(goals.data) ? goals.data.length : 0,
    memberCount: members.data?.data?.length ?? 0,
    pendingInvitationCount: invitations.data?.data?.length ?? 0,
    keys: keys.data?.data ?? [],
  })
  const progress = checklistProgress(items)
  if (progress.complete) return null

  const base = `/app/${orgSlug}/${siteSlug}`
  const hrefs: Record<ChecklistId, string> = {
    snippet: `${base}/settings/installation`,
    firstEvent: `${base}/realtime`,
    goal: `${base}/goals`,
    invite: `/app/${orgSlug}/settings/invites`,
    mcp: `/app/${orgSlug}/settings/api-keys?create=1`,
  }
  const text = uiOnboarding.checklist
  const firstEventMissing = items.find((item) => item.id === 'firstEvent')?.done === false

  return (
    <section aria-labelledby="getting-started-title" className="flex flex-col gap-3 rounded-xl border bg-card p-4 text-card-foreground shadow-sm">
      <div className="flex items-start justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <h2 id="getting-started-title" className="text-base font-semibold">{text.title}</h2>
          <p className="text-sm text-muted-foreground" data-testid="checklist-progress">
            {text.progress.replace('{done}', String(progress.done)).replace('{total}', String(progress.total))}
          </p>
        </div>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          aria-label={text.dismiss}
          onClick={() => { writeDismissed(userId, siteId); setDismissed(true) }}
        >
          <XIcon />
        </Button>
      </div>
      <div aria-hidden="true" className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className="h-full rounded-full bg-primary motion-safe:transition-[width]" style={{ width: `${(progress.done / progress.total) * 100}%` }} />
      </div>
      <ul aria-label={text.listLabel} className="flex flex-col divide-y">
        {items.map((item) => {
          const copy = text.items[item.id]
          return (
            <li key={item.id} className="flex flex-wrap items-center gap-3 py-2">
              {item.done
                ? <CheckCircle2Icon aria-hidden="true" className="size-4 shrink-0 text-success" />
                : <CircleIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />}
              <div className="flex min-w-0 flex-1 flex-col max-sm:flex-[1_1_calc(100%-1.75rem)]">
                <span className={item.done ? 'text-sm font-medium text-muted-foreground' : 'text-sm font-medium'}>
                  {copy.title}
                  <span className="sr-only"> ({item.done ? text.done : text.todo})</span>
                </span>
                {item.done ? null : <span className="text-xs text-muted-foreground">{copy.hint}</span>}
              </div>
              {item.done ? null : (
                <Button asChild size="sm" variant="outline" className="max-sm:ml-auto max-sm:min-h-11">
                  <Link to={hrefs[item.id]}>{copy.action}</Link>
                </Button>
              )}
            </li>
          )
        })}
      </ul>
      {firstEventMissing ? (
        <div>
          <Button type="button" variant="ghost" size="sm" disabled={demo.isPending} onClick={() => demo.mutate()}>
            {text.sampleData}
          </Button>
          {demo.isError ? <p role="alert" className="text-xs text-destructive">{demo.error instanceof Error ? demo.error.message : ''}</p> : null}
        </div>
      ) : null}
    </section>
  )
}
