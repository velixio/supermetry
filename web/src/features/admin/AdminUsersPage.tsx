import { useState, type FormEvent } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PasswordInput } from '@/features/auth/PasswordInput'
import { useCopyStatus } from '@/features/settings/InstallRecipes'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'
import {
  useAdminUsers, useCreateAdminUser, useImpersonateUser, useUpdateAdminUser,
  type AdminUser, type AdminUserCreated, type AdminUserPatch,
} from './adminApi'
import { AdminPage, ErrorLine, SearchForm, cellClass, formatDate, tableClass, useAdminSearch } from './AdminCommon'

const errorText = (error: unknown) => (error instanceof Error ? error.message : String(error))

function CreateUserDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (open: boolean) => void; onCreated: (email: string) => void }) {
  const create = useCreateAdminUser()
  const [sendReset, setSendReset] = useState(true)
  const [created, setCreated] = useState<AdminUserCreated | null>(null)
  const { copied, copy } = useCopyStatus()
  const close = (next: boolean) => {
    if (create.isPending) return
    if (!next) {
      create.reset()
      setCreated(null)
      setSendReset(true)
    }
    onOpenChange(next)
  }
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const value = (name: string) => String(data.get(name) ?? '').trim()
    create.mutate(
      { email: value('email'), ...(value('name') ? { name: value('name') } : {}), ...(value('org_name') ? { org_name: value('org_name') } : {}), send_reset: sendReset },
      {
        onSuccess: (user) => {
          onCreated(user.email)
          if (user.reset_url) setCreated(user)
          else close(false)
        },
      },
    )
  }
  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{created ? uiFlows.admin.linkTitle : uiFlows.admin.addUserTitle}</DialogTitle>
          <DialogDescription>{created ? uiFlows.admin.linkHint : uiFlows.admin.addUserDescription}</DialogDescription>
        </DialogHeader>
        {created?.reset_url ? (
          <div className="flex flex-col gap-3">
            <code className="rounded-md bg-muted px-3 py-2 text-xs break-all select-all" aria-label={uiFlows.admin.linkTitle}>{created.reset_url}</code>
            <div className="flex flex-wrap items-center gap-3">
              <Button type="button" variant="outline" onClick={() => void copy(created.reset_url ?? '')}>{uiFlows.common.copy}</Button>
              {copied ? <span role="status" className="text-sm text-muted-foreground">{copied === 'yes' ? uiFlows.common.copied : uiFlows.common.copyManual}</span> : null}
            </div>
            <DialogFooter><Button type="button" onClick={() => close(false)}>{uiFlows.common.close}</Button></DialogFooter>
          </div>
        ) : (
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <FieldGroup>
              <Field data-invalid={Boolean(create.error) || undefined}>
                <FieldLabel htmlFor="admin-new-email">{uiFlows.admin.email}</FieldLabel>
                <Input id="admin-new-email" name="email" type="email" required autoComplete="off" spellCheck={false} autoCapitalize="none" aria-invalid={Boolean(create.error) || undefined} aria-describedby={create.error ? 'admin-new-error' : undefined} />
              </Field>
              <Field>
                <FieldLabel htmlFor="admin-new-name">{uiFlows.admin.name}</FieldLabel>
                <Input id="admin-new-name" name="name" autoComplete="off" maxLength={200} aria-describedby="admin-new-name-hint" />
                <FieldDescription id="admin-new-name-hint">{uiFlows.admin.nameHint}</FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="admin-new-org">{uiFlows.admin.orgName}</FieldLabel>
                <Input id="admin-new-org" name="org_name" autoComplete="off" maxLength={100} aria-describedby="admin-new-org-hint" />
                <FieldDescription id="admin-new-org-hint">{uiFlows.admin.orgNameHint}</FieldDescription>
              </Field>
              <Field orientation="horizontal">
                <Checkbox id="admin-new-reset" checked={sendReset} onCheckedChange={(checked) => setSendReset(checked === true)} aria-describedby="admin-new-reset-hint" />
                <div className="flex flex-col gap-1">
                  <FieldLabel htmlFor="admin-new-reset" className="font-normal">{uiFlows.admin.sendReset}</FieldLabel>
                  <FieldDescription id="admin-new-reset-hint">{uiFlows.admin.sendResetHint}</FieldDescription>
                </div>
              </Field>
              {create.error ? <FieldError id="admin-new-error">{errorText(create.error)}</FieldError> : null}
            </FieldGroup>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={create.isPending} onClick={() => close(false)}>{uiFlows.common.cancel}</Button>
              <Button type="submit" disabled={create.isPending}>{create.isPending ? uiFlows.admin.creating : uiFlows.admin.create}</Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

function ImpersonateDialog({ user, adminTotpEnabled, onClose, onSignedIn }: { user: AdminUser | null; adminTotpEnabled: boolean; onClose: () => void; onSignedIn: () => void }) {
  const impersonate = useImpersonateUser()
  const close = () => {
    if (impersonate.isPending) return
    impersonate.reset()
    onClose()
  }
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!user) return
    const data = new FormData(event.currentTarget)
    impersonate.mutate(
      { id: user.id, password: String(data.get('password') ?? ''), totpCode: String(data.get('totp_code') ?? '') || undefined },
      { onSuccess: onSignedIn },
    )
  }
  return (
    <Dialog open={user !== null} onOpenChange={(open) => { if (!open) close() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{user ? uiFlows.admin.signInAsTitle(user.email) : uiFlows.admin.signInAs}</DialogTitle>
          <DialogDescription>{uiFlows.admin.signInAsBody}</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={submit}>
          <FieldGroup>
            <Field data-invalid={Boolean(impersonate.error) || undefined}>
              <FieldLabel htmlFor="impersonate-password">{uiFlows.admin.yourPassword}</FieldLabel>
              <PasswordInput id="impersonate-password" name="password" autoComplete="current-password" required aria-invalid={Boolean(impersonate.error) || undefined} aria-describedby={impersonate.error ? 'impersonate-error' : undefined} />
            </Field>
            {adminTotpEnabled ? (
              <Field>
                <FieldLabel htmlFor="impersonate-totp">{uiFlows.admin.yourCode}</FieldLabel>
                <Input id="impersonate-totp" name="totp_code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required aria-describedby="impersonate-totp-hint" />
                <FieldDescription id="impersonate-totp-hint">{uiFlows.admin.yourCodeHint}</FieldDescription>
              </Field>
            ) : null}
            {impersonate.error ? <FieldError id="impersonate-error">{errorText(impersonate.error)}</FieldError> : null}
          </FieldGroup>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={impersonate.isPending} onClick={close}>{uiFlows.common.cancel}</Button>
            <Button type="submit" disabled={impersonate.isPending}>{impersonate.isPending ? uiFlows.admin.signingIn : uiFlows.admin.signInAs}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

type PendingAction = { user: AdminUser; patch: AdminUserPatch; title: string; body: string } | null

export function AdminUsersPage({ currentUserId, adminTotpEnabled = false, search: urlSearch, onSearch, onImpersonated = () => window.location.assign('/app') }: {
  currentUserId?: number
  /** Whether the signed-in admin uses two-factor authentication (their code is then required to sign in as a user). */
  adminTotpEnabled?: boolean
  search?: string
  onSearch?: (value: string) => void
  onImpersonated?: () => void
}) {
  const [search, setSearch] = useAdminSearch(urlSearch, onSearch)
  const users = useAdminUsers(search)
  const update = useUpdateAdminUser()
  const [pending, setPending] = useState<PendingAction>(null)
  const [creating, setCreating] = useState(false)
  const [impersonating, setImpersonating] = useState<AdminUser | null>(null)
  const [notice, setNotice] = useState('')

  const act = (user: AdminUser, patch: AdminUserPatch) => {
    setNotice('')
    update.mutate({ id: user.id, patch }, { onSuccess: () => setNotice(uiFlows.admin.actionDone) })
  }

  return (
    <AdminPage title={en.admin.users}>
      <div className="flex flex-wrap items-end justify-between gap-3">
        <SearchForm onSearch={setSearch} initial={search} />
        <Button type="button" className="min-h-11" onClick={() => setCreating(true)}>{uiFlows.admin.addUser}</Button>
      </div>
      <ErrorLine error={users.error ?? (pending ? null : update.error)} />
      {notice ? <p role="status" className="text-sm text-muted-foreground">{notice}</p> : null}
      {users.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {users.data && users.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      {users.data && users.data.data.length > 0 ? (
        <div className="relative overflow-x-auto rounded-lg border" tabIndex={0} role="region" aria-label={en.admin.users}>
          <table className={tableClass}>
            <caption className="sr-only">{en.admin.users}</caption>
            <thead className="border-b bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr><th scope="col" className={cellClass}>User</th><th scope="col" className={cellClass}>Status</th><th scope="col" className={cellClass}>Last sign-in</th><th scope="col" className={cellClass}>Actions</th></tr>
            </thead>
            <tbody>
              {users.data.data.map((user) => {
                const self = user.id === currentUserId
                return (
                  <tr key={user.id} className="border-b last:border-0" data-testid="admin-user-row">
                    <td className={cellClass}><div className="font-medium">{user.name}</div><div className="break-all text-muted-foreground">{user.email}</div></td>
                    <td className={cellClass}>
                      <div className="flex flex-wrap gap-1">
                        {user.instance_admin ? <Badge variant="secondary">{en.admin.instanceAdmin}</Badge> : null}
                        {user.disabled ? <Badge variant="destructive">{en.admin.disabled}</Badge> : null}
                        {user.password_reset_required ? <Badge variant="outline">{en.admin.resetRequired}</Badge> : null}
                        {user.totp_enabled ? <Badge variant="outline">{en.admin.twoFactorOn}</Badge> : null}
                      </div>
                    </td>
                    <td className={cellClass}>{formatDate(user.last_login_at)}</td>
                    <td className={cellClass}>
                      <div className="flex flex-wrap gap-1">
                        <Button size="sm" variant="outline" className="min-h-11" disabled={update.isPending || self}
                          aria-label={`${user.disabled ? en.admin.enable : en.admin.disable} ${user.email}`}
                          onClick={() => user.disabled
                            ? act(user, { disabled: false })
                            : setPending({ user, patch: { disabled: true }, title: uiFlows.admin.disableTitle(user.email), body: en.admin.confirmDisable })}>
                          {user.disabled ? en.admin.enable : en.admin.disable}
                        </Button>
                        <Button size="sm" variant="outline" className="min-h-11" disabled={update.isPending || user.password_reset_required}
                          aria-label={`${en.admin.requireReset}: ${user.email}`} onClick={() => act(user, { password_reset_required: true })}>
                          {en.admin.requireReset}
                        </Button>
                        <Button size="sm" variant="outline" className="min-h-11" disabled={update.isPending || (self && user.instance_admin)}
                          aria-label={`${user.instance_admin ? en.admin.demote : en.admin.promote}: ${user.email}`}
                          onClick={() => user.instance_admin
                            ? setPending({ user, patch: { instance_admin: false }, title: uiFlows.admin.demoteTitle(user.email), body: en.admin.confirmDemote })
                            : act(user, { instance_admin: true })}>
                          {user.instance_admin ? en.admin.demote : en.admin.promote}
                        </Button>
                        <Button size="sm" variant="outline" className="min-h-11"
                          disabled={self || user.instance_admin || user.disabled || user.password_reset_required}
                          aria-label={uiFlows.admin.signInAsAria(user.email)} onClick={() => setImpersonating(user)}>
                          {uiFlows.admin.signInAs}
                        </Button>
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ) : null}
      <AlertDialog open={pending !== null} onOpenChange={(open) => { if (!open && !update.isPending) { update.reset(); setPending(null) } }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{pending?.title}</AlertDialogTitle>
            <AlertDialogDescription>{pending?.body}</AlertDialogDescription>
          </AlertDialogHeader>
          {pending ? <ErrorLine error={update.error} /> : null}
          <AlertDialogFooter>
            <AlertDialogCancel disabled={update.isPending}>{uiFlows.common.cancel}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={update.isPending} onClick={(event) => {
              event.preventDefault()
              if (!pending) return
              update.mutate({ id: pending.user.id, patch: pending.patch }, { onSuccess: () => { setPending(null); setNotice(uiFlows.admin.actionDone) } })
            }}>
              {update.isPending ? uiFlows.common.saving : uiFlows.admin.confirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <CreateUserDialog open={creating} onOpenChange={setCreating} onCreated={(email) => setNotice(uiFlows.admin.created(email))} />
      <ImpersonateDialog user={impersonating} adminTotpEnabled={adminTotpEnabled} onClose={() => setImpersonating(null)} onSignedIn={onImpersonated} />
    </AdminPage>
  )
}
