import { useState, type FormEvent } from 'react'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { useAdminAudit, type AuditFilters } from './adminApi'
import { AdminPage, ErrorLine, LoadMore, formatDate } from './AdminCommon'

const fields = [
  ['actor', 'Actor (user id)', 'number'],
  ['action', 'Action', 'text'],
  ['org', 'Organization (id)', 'number'],
  ['from', 'From', 'datetime-local'],
  ['to', 'To', 'datetime-local'],
] as const

export function AdminAuditPage() {
  const [filters, setFilters] = useState<AuditFilters>({})
  const audit = useAdminAudit(filters)
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const next: AuditFilters = {}
    for (const [name] of fields) {
      const value = String(data.get(name) ?? '').trim()
      if (value) next[name] = value
    }
    setFilters(next)
  }
  return (
    <AdminPage title={en.admin.auditTitle}>
      <form className="flex flex-wrap items-end gap-3" onSubmit={submit} aria-label="Audit filters">
        {fields.map(([name, label, type]) => (
          <label key={name} className="flex flex-col gap-1 text-sm font-medium">
            {label}
            <Input name={name} type={type} min={type === 'number' ? 1 : undefined} maxLength={200} className="w-44" />
          </label>
        ))}
        <Button type="submit" variant="outline" className="max-sm:min-h-11">Filter</Button>
        <Button type="reset" variant="ghost" className="max-sm:min-h-11" onClick={() => setFilters({})}>Clear</Button>
      </form>
      <ErrorLine error={audit.error} />
      {audit.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {audit.data && audit.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      <ul className="flex flex-col rounded-lg text-sm">
        {audit.data?.data.map((entry) => (
          <li key={entry.id} className="flex flex-wrap items-baseline justify-between gap-2 px-3 py-2">
            <span className="font-medium">{entry.action}</span>
            <span className="text-muted-foreground">{entry.actor_user_id ? `user #${entry.actor_user_id} · ` : ''}{entry.target_type} #{entry.target_id} · {formatDate(entry.created_at)}</span>
          </li>
        ))}
      </ul>
      <LoadMore query={audit} />
    </AdminPage>
  )
}
