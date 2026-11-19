import { Link } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { PasswordInput } from '@/features/auth/PasswordInput'
import { useCopyStatus } from '@/features/settings/InstallRecipes'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'
import { useAdminUser, useRevokeUserSessions, useSendPasswordReset, type AdminPasswordReset } from './adminApi'
import { AdminPage, ErrorLine, formatDate } from './AdminCommon'

export function AdminUserDetailPage({ userId, adminTotpEnabled = false }: { userId: number; adminTotpEnabled?: boolean }) {
  const user = useAdminUser(userId)
  const revoke = useRevokeUserSessions()
  const reset = useSendPasswordReset()
  const [confirm, setConfirm] = useState<'revoke' | 'reset' | null>(null)
  const [result, setResult] = useState<AdminPasswordReset | null>(null)
  const [notice, setNotice] = useState('')
  const { copied, copy } = useCopyStatus()
  const u = user.data
  const busy = revoke.isPending || reset.isPending
  const run = () => {
    revoke.mutate(userId, { onSuccess: () => { setConfirm(null); setNotice('Sessions revoked.') } })
  }
  const closeReset = () => {
    if (reset.isPending) return
    reset.reset()
    setConfirm(null)
  }
  const submitReset = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    reset.mutate(
      { id: userId, password: String(data.get('password') ?? ''), totpCode: String(data.get('totp_code') ?? '') || undefined },
      { onSuccess: (r) => { setConfirm(null); setResult(r); setNotice(r.sent ? 'Password reset email sent.' : '') } },
    )
  }
  return (
    <AdminPage title={u ? u.name || u.email : en.admin.users}>
      <Link to="/app/admin/users" className="text-sm underline">{en.admin.users}</Link>
      <ErrorLine error={user.error} />
      {user.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {u ? (
        <div className="flex flex-col gap-4">
          <div className="text-sm">
            <p className="break-all text-muted-foreground">{u.email}</p>
            <p>Last sign-in: {formatDate(u.last_login_at)}</p>
            <p>Active sessions: {u.active_sessions}</p>
            <p>Two-factor exempt: {u.two_factor_exempt ? 'yes' : 'no'}</p>
          </div>
          <section aria-labelledby="user-memberships" className="flex flex-col gap-1">
            <h2 id="user-memberships" className="text-sm font-semibold">Memberships ({u.memberships.length})</h2>
            {u.memberships.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
            <ul className="text-sm">{u.memberships.map((m) => <li key={m.org_id}>{m.org_name} <span className="text-muted-foreground">{m.role}</span></li>)}</ul>
          </section>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" className="max-sm:min-h-11" disabled={busy} onClick={() => { revoke.reset(); setNotice(''); setConfirm('revoke') }}>Revoke sessions</Button>
            <Button type="button" variant="outline" className="max-sm:min-h-11" disabled={busy} onClick={() => { reset.reset(); setNotice(''); setResult(null); setConfirm('reset') }}>Send password reset</Button>
          </div>
          {notice ? <p role="status" className="text-sm text-muted-foreground">{notice}</p> : null}
          {result?.reset_url ? (
            <div className="flex flex-col gap-2">
              <p className="text-sm text-muted-foreground">{uiFlows.admin.linkHint}</p>
              <code className="rounded-md bg-muted px-3 py-2 text-xs break-all select-all" aria-label={uiFlows.admin.linkTitle}>{result.reset_url}</code>
              <div className="flex items-center gap-3">
                <Button type="button" variant="outline" onClick={() => void copy(result.reset_url ?? '')}>{uiFlows.common.copy}</Button>
                {copied ? <span role="status" className="text-sm text-muted-foreground">{copied === 'yes' ? uiFlows.common.copied : uiFlows.common.copyManual}</span> : null}
              </div>
            </div>
          ) : null}
        </div>
      ) : null}
      <AlertDialog open={confirm === 'revoke'} onOpenChange={(open) => { if (!open && !busy) setConfirm(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke all sessions?</AlertDialogTitle>
            <AlertDialogDescription>The user is signed out everywhere and must sign in again.</AlertDialogDescription>
          </AlertDialogHeader>
          <ErrorLine error={revoke.error} />
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>{uiFlows.common.cancel}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={busy} onClick={(event) => { event.preventDefault(); run() }}>{busy ? uiFlows.common.saving : uiFlows.admin.confirm}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      <Dialog open={confirm === 'reset'} onOpenChange={(open) => { if (!open) closeReset() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Send a password reset?</DialogTitle>
            <DialogDescription>A one-time link to set a new password is emailed, or shown here when no email is configured. Confirm it is you first.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" onSubmit={submitReset}>
            <FieldGroup>
              <Field data-invalid={Boolean(reset.error) || undefined}>
                <FieldLabel htmlFor="reset-password">{uiFlows.admin.yourPassword}</FieldLabel>
                <PasswordInput id="reset-password" name="password" autoComplete="current-password" required aria-invalid={Boolean(reset.error) || undefined} aria-describedby={reset.error ? 'reset-error' : undefined} />
              </Field>
              {adminTotpEnabled ? (
                <Field>
                  <FieldLabel htmlFor="reset-totp">{uiFlows.admin.yourCode}</FieldLabel>
                  <Input id="reset-totp" name="totp_code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required aria-describedby="reset-totp-hint" />
                  <FieldDescription id="reset-totp-hint">{uiFlows.admin.yourCodeHint}</FieldDescription>
                </Field>
              ) : null}
              {reset.error ? <FieldError id="reset-error">{reset.error instanceof Error ? reset.error.message : String(reset.error)}</FieldError> : null}
            </FieldGroup>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={reset.isPending} onClick={closeReset}>{uiFlows.common.cancel}</Button>
              <Button type="submit" disabled={reset.isPending}>{reset.isPending ? uiFlows.common.saving : uiFlows.admin.confirm}</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </AdminPage>
  )
}
