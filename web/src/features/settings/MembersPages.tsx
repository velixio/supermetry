import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { useState, type ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { activeLocale } from '@/lib/i18n/intl'
import { uiFlows } from '@/messages/uiFlows'

import { type MemberRole } from './MemberRoleSelect'
import { MemberAccessControls, siteAccessLabel, type MemberAccess } from './MemberAccessControls'
import { useCopyStatus } from './InstallRecipes'
import { privateRequest, type ListResponse } from './privateApi'

type Member = {
  user_id: number
  email: string
  name: string
  role: MemberRole
  site_ids: number[] | null
  capabilities: string[]
  billing_admin: boolean
}

type Invitation = {
  id: number
  email: string
  role: MemberRole
  expires_at: string
  accepted_at?: string
  revoked_at?: string
  site_ids: number[] | null
  capabilities: string[]
  billing_admin: boolean
}

const roleRank: Record<MemberRole, number> = { viewer: 0, editor: 1, admin: 2, owner: 3 }

/** The fields of `next` that differ from `current`, so a save never rewrites untouched grants. */
export function accessChanges(current: MemberAccess, next: MemberAccess): Partial<MemberAccess> {
  const changes: Partial<MemberAccess> = {}
  if (next.role !== current.role) changes.role = next.role
  const sameSites = current.site_ids === null || next.site_ids === null
    ? current.site_ids === next.site_ids
    : current.site_ids.length === next.site_ids.length && current.site_ids.every((id) => next.site_ids!.includes(id))
  if (!sameSites) changes.site_ids = next.site_ids
  const sameCapabilities = current.capabilities.length === next.capabilities.length && current.capabilities.every((item) => next.capabilities.includes(item))
  if (!sameCapabilities) changes.capabilities = next.capabilities
  return changes
}

/** True when a change takes away something the member has now: a lower role, fewer sites or grants. */
export function reducesAccess(current: MemberAccess, next: MemberAccess): boolean {
  if (roleRank[next.role] < roleRank[current.role]) return true
  if (next.site_ids !== null && (current.site_ids === null || current.site_ids.some((id) => !next.site_ids!.includes(id)))) return true
  return current.capabilities.some((item) => !next.capabilities.includes(item))
}

function Page({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <PageContainer width="narrow">
      <PageHeader title={title} description={description} />
      {children}
    </PageContainer>
  )
}

function Surface({ children, label }: { children: ReactNode; label?: string }) {
  return <section aria-label={label} className="flex flex-col gap-5 rounded-xl border bg-card p-5 text-card-foreground">{children}</section>
}

function ErrorLine({ error }: { error: unknown }) {
  if (!error) return null
  return <p role="alert" className="text-sm text-destructive">{error instanceof Error ? error.message : String(error)}</p>
}

function Notice({ children }: { children: ReactNode }) {
  return <p role="status" className="text-sm text-muted-foreground">{children}</p>
}

function ConfirmDialog({ open, title, body, confirm, destructive = true, pending = false, error, onConfirm, onCancel }: {
  open: boolean; title: string; body: string; confirm: string; destructive?: boolean; pending?: boolean; error?: unknown
  onConfirm: () => void; onCancel: () => void
}) {
  return (
    <AlertDialog open={open} onOpenChange={(next) => { if (!next && !pending) onCancel() }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{body}</AlertDialogDescription>
        </AlertDialogHeader>
        <ErrorLine error={error} />
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>{uiFlows.common.cancel}</AlertDialogCancel>
          <AlertDialogAction variant={destructive ? 'destructive' : 'default'} disabled={pending} onClick={(event) => { event.preventDefault(); onConfirm() }}>
            {pending ? uiFlows.common.saving : confirm}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function accessOf(member: Member): MemberAccess {
  return { role: member.role, site_ids: member.site_ids ?? null, capabilities: member.capabilities ?? [] }
}

function MemberRow({ member, isSelf, actorRole, sites, orgId, onSaved, onRemoved }: {
  member: Member; isSelf: boolean; actorRole: MemberRole; sites: { id: number; name: string }[]; orgId: number
  onSaved: (name: string) => void; onRemoved: (name: string) => void
}) {
  const queryClient = useQueryClient()
  const original = accessOf(member)
  const [draft, setDraft] = useState<MemberAccess>(original)
  const [confirming, setConfirming] = useState<'reduce' | 'remove' | null>(null)
  const changes = accessChanges(original, draft)
  const dirty = Object.keys(changes).length > 0
  const display = member.name || member.email
  const locked = member.role === 'owner' && actorRole !== 'owner'
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ['members', orgId] })
    void queryClient.invalidateQueries({ queryKey: ['me'] })
  }
  const update = useMutation({
    mutationFn: (body: Partial<MemberAccess>) => privateRequest<void>('PATCH', `/api/private/orgs/${orgId}/members/${member.user_id}`, { orgId, body }),
    onSuccess: () => {
      setConfirming(null)
      onSaved(display)
      refresh()
    },
  })
  const remove = useMutation({
    mutationFn: () => privateRequest<void>('DELETE', `/api/private/orgs/${orgId}/members/${member.user_id}`, { orgId }),
    onSuccess: () => {
      setConfirming(null)
      onRemoved(display)
      refresh()
    },
  })
  const save = () => {
    if (reducesAccess(original, draft)) setConfirming('reduce')
    else update.mutate(changes)
  }
  return (
    <li className="flex flex-col gap-4 border-b pb-6 last:border-0 last:pb-0">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-sm font-medium">{member.name}{isSelf ? <span className="ml-1 font-normal text-muted-foreground">{uiFlows.members.you}</span> : null}</p>
          <p className="break-all text-xs text-muted-foreground">{member.email}</p>
        </div>
        {!isSelf && !locked ? (
          <Button type="button" size="sm" variant="outline" aria-label={uiFlows.members.removeAria(display)} disabled={remove.isPending} onClick={() => { remove.reset(); setConfirming('remove') }}>
            {uiFlows.members.remove}
          </Button>
        ) : null}
      </div>
      <MemberAccessControls
        value={draft}
        actorRole={actorRole}
        sites={sites}
        disabled={locked || update.isPending}
        onChange={(next) => setDraft((current) => ({ ...current, ...next }))}
      />
      {update.error && confirming === null ? <ErrorLine error={update.error} /> : null}
      {dirty ? (
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" disabled={update.isPending} onClick={save}>{update.isPending ? uiFlows.common.saving : uiFlows.common.saveChanges}</Button>
          <Button type="button" size="sm" variant="ghost" disabled={update.isPending} onClick={() => { update.reset(); setDraft(original) }}>{uiFlows.members.discard}</Button>
        </div>
      ) : null}
      <ConfirmDialog
        open={confirming === 'reduce'}
        title={uiFlows.members.reduceTitle(display)}
        body={uiFlows.members.reduceBody}
        confirm={uiFlows.members.reduceConfirm}
        pending={update.isPending}
        error={update.error}
        onConfirm={() => update.mutate(changes)}
        onCancel={() => { update.reset(); setConfirming(null) }}
      />
      <ConfirmDialog
        open={confirming === 'remove'}
        title={uiFlows.members.removeTitle(display)}
        body={uiFlows.members.removeBody}
        confirm={uiFlows.members.removeConfirm}
        pending={remove.isPending}
        error={remove.error}
        onConfirm={() => remove.mutate()}
        onCancel={() => setConfirming(null)}
      />
    </li>
  )
}

/** Lets any member remove themselves; the last owner is told what to do instead. */
export function LeaveOrganization({ orgId, orgName, onLeft = () => window.location.assign('/app') }: { orgId: number; orgName: string; onLeft?: () => void }) {
  const [open, setOpen] = useState(false)
  const leave = useMutation({
    mutationFn: () => privateRequest<void>('DELETE', `/api/private/orgs/${orgId}/membership`, { orgId }),
    onSuccess: () => {
      setOpen(false)
      onLeft()
    },
  })
  const lastOwner = leave.error && typeof leave.error === 'object' && 'status' in leave.error && (leave.error as { status?: number }).status === 409
  return (
    <section aria-labelledby="leave-org-title" className="flex flex-col gap-3 rounded-xl border border-destructive/30 p-5">
      <h2 id="leave-org-title" className="text-base font-semibold">{uiFlows.members.leaveTitle}</h2>
      <p className="text-sm text-muted-foreground">{uiFlows.members.leaveDescription}</p>
      <Button type="button" variant="outline" className="self-start" onClick={() => { leave.reset(); setOpen(true) }}>{uiFlows.members.leave}</Button>
      <ConfirmDialog
        open={open}
        title={uiFlows.members.leaveConfirmTitle(orgName)}
        body={uiFlows.members.leaveConfirmBody}
        confirm={uiFlows.members.leaveConfirm}
        pending={leave.isPending}
        error={lastOwner ? new Error(uiFlows.members.lastOwner) : leave.error}
        onConfirm={() => leave.mutate()}
        onCancel={() => setOpen(false)}
      />
    </section>
  )
}

export function MembersPage({ orgId, orgName = '', actorRole, sites, currentUserId }: {
  orgId: number
  orgName?: string
  actorRole: MemberRole
  sites: { id: number; name: string }[]
  currentUserId?: number
}) {
  const canManage = actorRole === 'owner' || actorRole === 'admin'
  const [notice, setNotice] = useState('')
  const members = useQuery({
    queryKey: ['members', orgId],
    enabled: canManage,
    queryFn: () => privateRequest<ListResponse<Member>>('GET', `/api/private/orgs/${orgId}/members`, { orgId }),
  })
  if (!canManage) {
    return (
      <Page title="Members" description="Assign roles, project access, and sensitive data permissions.">
        <p role="alert">Only organization owners and admins can manage members.</p>
        <LeaveOrganization orgId={orgId} orgName={orgName} />
      </Page>
    )
  }
  const list = members.data?.data ?? []
  const others = list.filter((member) => member.user_id !== currentUserId)
  return (
    <Page title="Members" description="Assign roles, project access, and sensitive data permissions.">
      <div className="flex flex-col gap-6">
        <Surface>
          <ErrorLine error={members.error} />
          {notice ? <Notice>{notice}</Notice> : null}
          <ul className="flex flex-col gap-6">
            {list.map((member) => (
              <MemberRow
                key={`${member.user_id}:${member.role}:${(member.site_ids ?? ['all']).join(',')}:${(member.capabilities ?? []).join(',')}`}
                member={member}
                isSelf={member.user_id === currentUserId}
                actorRole={actorRole}
                sites={sites}
                orgId={orgId}
                onSaved={(name) => setNotice(uiFlows.members.saved(name))}
                onRemoved={(name) => setNotice(uiFlows.members.removed(name))}
              />
            ))}
          </ul>
          {members.isSuccess && others.length === 0 ? <p className="text-sm text-muted-foreground">{uiFlows.members.empty}</p> : null}
        </Surface>
        <LeaveOrganization orgId={orgId} orgName={orgName} />
      </div>
    </Page>
  )
}

function invitationState(invitation: Invitation, now: number): 'pending' | 'accepted' | 'revoked' | 'expired' {
  if (invitation.accepted_at) return 'accepted'
  if (invitation.revoked_at) return 'revoked'
  return new Date(invitation.expires_at).getTime() <= now ? 'expired' : 'pending'
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleDateString(activeLocale(), { dateStyle: 'medium' })
}

export function InvitationsPage({ orgId, actorRole = 'viewer', sites = [], now = Date.now() }: {
  orgId: number
  actorRole?: MemberRole
  sites?: { id: number; name: string }[]
  now?: number
}) {
  const queryClient = useQueryClient()
  const [link, setLink] = useState('')
  const [notice, setNotice] = useState('')
  const [revoking, setRevoking] = useState<Invitation | null>(null)
  const [access, setAccess] = useState<MemberAccess>({ role: 'viewer', site_ids: null, capabilities: [] })
  const { copied, copy } = useCopyStatus()
  const canManage = actorRole === 'owner' || actorRole === 'admin'
  const key = ['invitations', orgId]
  const invitations = useQuery({
    queryKey: key,
    queryFn: () => privateRequest<ListResponse<Invitation>>('GET', `/api/private/orgs/${orgId}/invitations`, { orgId }),
    enabled: canManage,
  })
  const createInvitation = (email: string, grant: MemberAccess) =>
    privateRequest<{ link: string }>('POST', `/api/private/orgs/${orgId}/invitations`, {
      orgId,
      body: { email, role: grant.role, site_ids: grant.site_ids, capabilities: grant.capabilities },
    })
  const create = useMutation({
    mutationFn: (email: string) => createInvitation(email, access),
    onSuccess: (data, email) => {
      setLink(data.link)
      setNotice(uiFlows.invitations.sent(email))
      void queryClient.invalidateQueries({ queryKey: key })
    },
  })
  const revoke = useMutation({
    mutationFn: (invitation: Invitation) => privateRequest<void>('DELETE', `/api/private/orgs/${orgId}/invitations/${invitation.id}`, { orgId }),
    onSuccess: (_, invitation) => {
      setRevoking(null)
      setNotice(uiFlows.invitations.revokedNotice(invitation.email))
      void queryClient.invalidateQueries({ queryKey: key })
    },
  })
  // A fresh invitation with the same access replaces the old link, which is then revoked.
  const resend = useMutation({
    mutationFn: async (invitation: Invitation) => {
      const created = await createInvitation(invitation.email, { role: invitation.role, site_ids: invitation.site_ids ?? null, capabilities: invitation.capabilities ?? [] })
      if (!invitation.revoked_at && !invitation.accepted_at) {
        await privateRequest<void>('DELETE', `/api/private/orgs/${orgId}/invitations/${invitation.id}`, { orgId })
      }
      return created
    },
    onSuccess: (data, invitation) => {
      setLink(data.link)
      setNotice(uiFlows.invitations.resent(invitation.email))
      void queryClient.invalidateQueries({ queryKey: key })
    },
  })
  if (!canManage) return <p role="alert">Only organization owners and admins can manage invitations.</p>
  const list = invitations.data?.data ?? []
  return (
    <Page title="Invitations" description="Invite a teammate with the project access and permissions they need.">
      <div className="flex flex-col gap-6">
        <Surface>
          <form
            className="flex flex-col gap-5"
            onSubmit={(event) => {
              event.preventDefault()
              const form = event.currentTarget
              setNotice('')
              create.mutate(String(new FormData(form).get('email') ?? '').trim(), { onSuccess: () => form.reset() })
            }}
          >
            <Field data-invalid={Boolean(create.error) || undefined}>
              <FieldLabel htmlFor="invite-email">Email</FieldLabel>
              <Input
                id="invite-email"
                name="email"
                type="email"
                autoComplete="off"
                spellCheck={false}
                autoCapitalize="none"
                placeholder="teammate@example.com…"
                required
                aria-invalid={Boolean(create.error) || undefined}
                aria-describedby={create.error ? 'invite-email-error' : undefined}
              />
              {create.error ? <FieldError id="invite-email-error">{create.error.message}</FieldError> : null}
            </Field>
            <MemberAccessControls
              allowOwner={false}
              value={access}
              actorRole={actorRole}
              sites={sites}
              disabled={create.isPending}
              onChange={(changes) => setAccess((current) => ({ ...current, ...changes }))}
            />
            <Button className="self-start" type="submit" disabled={create.isPending}>{create.isPending ? uiFlows.common.saving : 'Invite'}</Button>
          </form>
          {notice ? <Notice>{notice}</Notice> : null}
          {link ? (
            <div className="flex flex-wrap items-center gap-3">
              <Button className="self-start" variant="outline" onClick={() => void copy(link)}>{uiFlows.invitations.copyLink}</Button>
              {copied ? <span role="status" className="text-sm text-muted-foreground">{copied === 'yes' ? uiFlows.common.copied : uiFlows.common.copyManual}</span> : null}
            </div>
          ) : null}
        </Surface>
        <Surface label={uiFlows.invitations.heading}>
          <h2 className="text-base font-semibold">{uiFlows.invitations.heading}</h2>
          <ErrorLine error={invitations.error || resend.error} />
          {invitations.isSuccess && list.length === 0 ? <p className="text-sm text-muted-foreground">{uiFlows.invitations.empty}</p> : null}
          <ul className="flex flex-col gap-2">
            {list.map((invitation) => {
              const state = invitationState(invitation, now)
              return (
                <li className="flex flex-wrap items-center justify-between gap-3 border-t pt-3 first:border-0 first:pt-0" key={invitation.id}>
                  <div className="min-w-0 text-sm">
                    <p className="flex flex-wrap items-center gap-2">
                      <span className="break-all">{invitation.email} · {invitation.role}</span>
                      <Badge variant={state === 'pending' ? 'secondary' : 'outline'}>{uiFlows.invitations[state]}</Badge>
                    </p>
                    <p className="text-xs text-muted-foreground">
                      {siteAccessLabel(invitation.site_ids)}
                      {invitation.capabilities?.length ? ` · ${invitation.capabilities.join(', ')}` : ''}
                      {state === 'pending' ? ` · ${uiFlows.invitations.expires(formatDate(invitation.expires_at))}` : ''}
                    </p>
                  </div>
                  <div className="flex flex-wrap gap-2">
                    {state === 'pending' || state === 'expired' ? (
                      <Button size="sm" variant="outline" aria-label={uiFlows.invitations.resendAria(invitation.email)} disabled={resend.isPending} onClick={() => { setNotice(''); resend.mutate(invitation) }}>
                        {uiFlows.invitations.resend}
                      </Button>
                    ) : null}
                    {state === 'pending' ? (
                      <Button size="sm" variant="outline" aria-label={uiFlows.invitations.revokeAria(invitation.email)} disabled={revoke.isPending} onClick={() => { revoke.reset(); setRevoking(invitation) }}>
                        {uiFlows.invitations.revoke}
                      </Button>
                    ) : null}
                  </div>
                </li>
              )
            })}
          </ul>
        </Surface>
      </div>
      <ConfirmDialog
        open={revoking !== null}
        title={uiFlows.invitations.revokeTitle(revoking?.email ?? '')}
        body={uiFlows.invitations.revokeBody}
        confirm={uiFlows.invitations.revokeConfirm}
        pending={revoke.isPending}
        error={revoke.error}
        onConfirm={() => { if (revoking) revoke.mutate(revoking) }}
        onCancel={() => setRevoking(null)}
      />
    </Page>
  )
}
