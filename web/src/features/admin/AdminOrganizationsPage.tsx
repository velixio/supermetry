import { useState } from 'react'

import { Button } from '@/components/ui/button'
import { en } from '@/messages/en'
import { useAdminOrganization, useAdminOrganizations } from './adminApi'
import { AdminPage, ErrorLine, SearchForm, cellClass, formatDate, tableClass } from './AdminCommon'

function OrganizationDetail({ id, onClose }: { id: number; onClose: () => void }) {
  const detail = useAdminOrganization(id)
  return (
    <section aria-label={detail.data ? `${detail.data.name} ${en.admin.details}` : en.admin.details} className="flex flex-col gap-3 rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-lg font-semibold">{detail.data?.name ?? en.admin.loading}</h2>
        <Button variant="outline" className="min-h-11" onClick={onClose}>{en.admin.close}</Button>
      </div>
      <ErrorLine error={detail.error} />
      {detail.data ? (
        <div className="grid gap-4 md:grid-cols-2">
          <div>
            <h3 className="mb-1 text-sm font-medium">{en.admin.members} ({detail.data.members.length})</h3>
            <ul className="flex flex-col gap-1 text-sm">{detail.data.members.map((member) => <li key={member.id} className="break-all">{member.name} <span className="text-muted-foreground">{member.email}</span></li>)}</ul>
          </div>
          <div>
            <h3 className="mb-1 text-sm font-medium">{en.admin.sites} ({detail.data.sites.length})</h3>
            <ul className="flex flex-col gap-1 text-sm">{detail.data.sites.map((site) => <li key={site.id}>{site.name} <span className="text-muted-foreground">{formatDate(site.last_event_at)}</span></li>)}</ul>
          </div>
        </div>
      ) : null}
    </section>
  )
}

export function AdminOrganizationsPage() {
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<number | null>(null)
  const organizations = useAdminOrganizations(search)
  return (
    <AdminPage title={en.admin.organizations}>
      <SearchForm onSearch={setSearch} />
      <ErrorLine error={organizations.error} />
      {organizations.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {organizations.data && organizations.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      {organizations.data && organizations.data.data.length > 0 ? (
        <div className="overflow-x-auto rounded-lg border">
          <table className={tableClass}>
            <caption className="sr-only">{en.admin.organizations}</caption>
            <thead className="border-b bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr><th scope="col" className={cellClass}>Organization</th><th scope="col" className={cellClass}>{en.admin.members}</th><th scope="col" className={cellClass}>{en.admin.quota}</th><th scope="col" className={cellClass}><span className="sr-only">{en.admin.details}</span></th></tr>
            </thead>
            <tbody>
              {organizations.data.data.map((org) => (
                <tr key={org.id} className="border-b last:border-0">
                  <td className={cellClass}><div className="font-medium">{org.name}</div><div className="text-muted-foreground">{org.slug}</div></td>
                  <td className={cellClass}>{org.member_count}</td>
                  <td className={cellClass}>{org.site_count} / {org.site_limit}</td>
                  <td className={cellClass}><Button size="sm" variant="outline" className="min-h-11" aria-label={`${en.admin.details}: ${org.name}`} onClick={() => setSelected(org.id)}>{en.admin.details}</Button></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {selected !== null ? <OrganizationDetail id={selected} onClose={() => setSelected(null)} /> : null}
    </AdminPage>
  )
}
