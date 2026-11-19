import { Link } from '@tanstack/react-router'
import { useState } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { useCopyStatus } from '@/features/settings/InstallRecipes'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'
import { useAdminUser, useRevokeUserSessions, useSendPasswordReset, type AdminPasswordReset } from './adminApi'
import { AdminPage, ErrorLine, formatDate } from './AdminCommon'

export function AdminUserDetailPage({ userId }: { userId: number }) {
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
    if (confirm === 'revoke') revoke.mutate(userId, { onSuccess: () => { setConfirm(null); setNotice('Sessions revoked.') } })
    else reset.mutate(userId, { onSuccess: (r) => { setConfirm(null); setResult(r); setNotice(r.sent ? 'Password reset email sent.' : '') } })
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
      <AlertDialog open={confirm !== null} onOpenChange={(open) => { if (!open && !busy) setConfirm(null) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm === 'revoke' ? 'Revoke all sessions?' : 'Send a password reset?'}</AlertDialogTitle>
            <AlertDialogDescription>{confirm === 'revoke' ? 'The user is signed out everywhere and must sign in again.' : 'A one-time link to set a new password is emailed, or shown here when no email is configured.'}</AlertDialogDescription>
          </AlertDialogHeader>
          <ErrorLine error={revoke.error ?? reset.error} />
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy}>{uiFlows.common.cancel}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={busy} onClick={(event) => { event.preventDefault(); run() }}>{busy ? uiFlows.common.saving : uiFlows.admin.confirm}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </AdminPage>
  )
}
