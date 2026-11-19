import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { CheckCircle2Icon, ChevronDownIcon, CircleIcon, XIcon } from 'lucide-react'

import { Button } from '@/components/ui/button'
import { useCreateDemo } from '@/features/demo/useDemo'
import { useGoals } from '@/features/goals/api'
import { privateRequest, type ListResponse } from '@/features/settings/privateApi'
import type { ManagedSite as SiteDetails } from '@/features/sites/types'
import { en } from '@/messages/en'
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
  const [open, setOpen] = useState(false)
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
  const strip = en.setupStrip
  const firstEventMissing = items.find((item) => item.id === 'firstEvent')?.done === false
  const next = items.find((item) => !item.done)
  const nextCopy = next ? text.items[next.id] : null

  return (
    <section aria-labelledby="getting-started-title" className="rounded-lg border bg-card px-4 py-2 text-card-foreground">
      <div className="flex min-h-8 flex-wrap items-center gap-x-3 gap-y-1">
        <h2 id="getting-started-title" className="text-sm font-medium" data-testid="checklist-progress">
          {strip.progress.replace('{done}', String(progress.done)).replace('{total}', String(progress.total))}
        </h2>
        {next && nextCopy ? (
          <p className="min-w-0 flex-1 truncate text-sm text-muted-foreground">
            {strip.next.replace('{step}', nextCopy.title)}
          </p>
        ) : <span className="flex-1" />}
        {next && nextCopy ? (
          <Button asChild size="sm" variant="secondary" className="max-sm:min-h-11">
            <Link to={hrefs[next.id]}>{nextCopy.action}</Link>
          </Button>
        ) : null}
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="max-sm:min-h-11"
          aria-expanded={open}
          aria-controls="getting-started-list"
          onClick={() => setOpen((value) => !value)}
        >
          {open ? strip.hideAll : strip.showAll}
          <ChevronDownIcon aria-hidden="true" className={open ? 'rotate-180' : undefined} />
        </Button>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          className="max-sm:size-11"
          aria-label={text.dismiss}
          onClick={() => { writeDismissed(userId, siteId); setDismissed(true) }}
        >
          <XIcon />
        </Button>
      </div>
      {open ? (
        <div id="getting-started-list" className="flex flex-col gap-2 pt-2">
          <ul aria-label={text.listLabel} className="flex flex-col">
            {items.map((item) => {
              const copy = text.items[item.id]
              return (
                <li key={item.id} className="flex min-h-8 flex-wrap items-center gap-3 py-1">
                  {item.done
                    ? <CheckCircle2Icon aria-hidden="true" className="size-4 shrink-0 text-success" />
                    : <CircleIcon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />}
                  <span className={item.done ? 'min-w-0 flex-1 text-sm text-muted-foreground' : 'min-w-0 flex-1 text-sm'}>
                    {copy.title}
                    <span className="sr-only"> ({item.done ? text.done : text.todo})</span>
                  </span>
                  {item.done ? null : (
                    <Button asChild size="sm" variant="ghost" className="max-sm:min-h-11">
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
        </div>
      ) : null}
    </section>
  )
}
