import { useEffect, useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { useAdminAudit, useAdminUsers, useRemoveExemption, useSetExemption, useTwoFactorPolicy, useUpdateTwoFactorPolicy, type AdminTwoFactorPolicy } from './adminApi'
import { AdminPage, ErrorLine, formatDate } from './AdminCommon'

const scopes: { value: AdminTwoFactorPolicy['scope']; label: string }[] = [
  { value: 'off', label: en.admin.scopeOff },
  { value: 'instance_admins', label: en.admin.scopeAdmins },
  { value: 'owners_admins', label: en.admin.scopeOwners },
  { value: 'everyone', label: en.admin.scopeEveryone },
]

function PolicyForm() {
  const policy = useTwoFactorPolicy()
  const save = useUpdateTwoFactorPolicy()
  const [scope, setScope] = useState<AdminTwoFactorPolicy['scope']>('off')
  const [grace, setGrace] = useState('7')
  useEffect(() => {
    if (policy.data) {
      setScope(policy.data.scope)
      setGrace(String(policy.data.grace_days))
    }
  }, [policy.data])
  const submit = (event: FormEvent) => {
    event.preventDefault()
    save.mutate({ scope, grace_days: Math.min(30, Math.max(0, Number(grace) || 0)) })
  }
  return (
    <form onSubmit={submit} className="flex max-w-xl flex-col gap-4 rounded-lg border bg-card p-4" aria-labelledby="admin-policy-title">
      <h2 id="admin-policy-title" className="text-lg font-semibold">{en.admin.policyTitle}</h2>
      <ErrorLine error={policy.error ?? save.error} />
      <fieldset className="flex flex-col gap-1">
        <legend className="mb-1 text-sm font-medium">{en.admin.policyScope}</legend>
        {scopes.map((item) => (
          <label key={item.value} className="flex min-h-11 items-center gap-3 text-sm">
            <input type="radio" name="two-factor-scope" className="size-5" value={item.value} checked={scope === item.value} onChange={() => setScope(item.value)} />
            {item.label}
          </label>
        ))}
      </fieldset>
      <label className="flex max-w-40 flex-col gap-1 text-sm font-medium">
        {en.admin.graceDays}
        <Input type="number" min={0} max={30} value={grace} onChange={(event) => setGrace(event.target.value)} />
      </label>
      <p className="text-sm text-muted-foreground">{en.admin.policyHelp}</p>
      <div className="flex items-center gap-3">
        <Button type="submit" className="min-h-11" disabled={save.isPending || policy.isPending}>{en.admin.savePolicy}</Button>
        {save.isSuccess ? <span role="status" className="text-sm text-muted-foreground">{en.admin.saved}</span> : null}
      </div>
    </form>
  )
}

function ExemptionForm() {
  const users = useAdminUsers('')
  const grant = useSetExemption()
  const remove = useRemoveExemption()
  const [userId, setUserId] = useState('')
  const [reason, setReason] = useState('')
  const [expires, setExpires] = useState('')
  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!userId) return
    grant.mutate({ userId: Number(userId), exemption: { reason: reason.trim(), expires_at: expires ? new Date(expires).toISOString() : undefined } })
  }
  return (
    <form onSubmit={submit} className="flex max-w-xl flex-col gap-4 rounded-lg border bg-card p-4" aria-labelledby="admin-exemption-title">
      <h2 id="admin-exemption-title" className="text-lg font-semibold">{en.admin.exemptionTitle}</h2>
      <ErrorLine error={grant.error ?? remove.error} />
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.exemptionUser}
        <select className="h-11 rounded-lg border border-input bg-background px-3" value={userId} onChange={(event) => setUserId(event.target.value)}>
          <option value="">Choose a user</option>
          {users.data?.data.map((user) => <option key={user.id} value={user.id}>{user.email}</option>)}
        </select>
      </label>
      <label className="flex flex-col gap-1 text-sm font-medium">
        {en.admin.exemptionReason}
        <Input required maxLength={500} value={reason} onChange={(event) => setReason(event.target.value)} />
      </label>
      <label className="flex max-w-xs flex-col gap-1 text-sm font-medium">
        {en.admin.exemptionExpires}
        <Input type="datetime-local" value={expires} onChange={(event) => setExpires(event.target.value)} />
      </label>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" className="min-h-11" disabled={!userId || !reason.trim() || grant.isPending}>{en.admin.grantExemption}</Button>
        <Button type="button" variant="outline" className="min-h-11" disabled={!userId || remove.isPending} onClick={() => remove.mutate(Number(userId))}>{en.admin.removeExemption}</Button>
        {grant.isSuccess || remove.isSuccess ? <span role="status" className="text-sm text-muted-foreground">{en.admin.saved}</span> : null}
      </div>
    </form>
  )
}

function AuditList() {
  const audit = useAdminAudit()
  return (
    <section aria-labelledby="admin-audit-title" className="flex flex-col gap-2">
      <h2 id="admin-audit-title" className="text-lg font-semibold">{en.admin.auditTitle}</h2>
      <ErrorLine error={audit.error} />
      {audit.data && audit.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      <ul className="flex flex-col divide-y rounded-lg border text-sm">
        {audit.data?.data.map((entry) => (
          <li key={entry.id} className="flex flex-wrap items-baseline justify-between gap-2 px-3 py-2">
            <span className="font-medium">{entry.action}</span>
            <span className="text-muted-foreground">{entry.target_type} #{entry.target_id} · {formatDate(entry.created_at)}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}

export function AdminSecurityPage() {
  return (
    <AdminPage title={en.admin.security}>
      <PolicyForm />
      <ExemptionForm />
      <AuditList />
    </AdminPage>
  )
}
