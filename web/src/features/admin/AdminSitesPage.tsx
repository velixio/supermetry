import { useState } from 'react'

import { en } from '@/messages/en'
import { useAdminSites } from './adminApi'
import { AdminPage, ErrorLine, SearchForm, cellClass, formatDate, tableClass } from './AdminCommon'

export function AdminSitesPage() {
  const [search, setSearch] = useState('')
  const sites = useAdminSites(search)
  return (
    <AdminPage title={en.admin.sites}>
      <SearchForm onSearch={setSearch} />
      <ErrorLine error={sites.error} />
      {sites.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {sites.data && sites.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      {sites.data && sites.data.data.length > 0 ? (
        <div className="relative overflow-x-auto rounded-lg border" tabIndex={0}>
          <table className={tableClass}>
            <caption className="sr-only">{en.admin.sites}</caption>
            <thead className="border-b bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr>
                <th scope="col" className={cellClass}>Site</th><th scope="col" className={cellClass}>Organization</th>
                <th scope="col" className={cellClass}>{en.admin.usage}</th><th scope="col" className={cellClass}>{en.admin.lastEvent}</th><th scope="col" className={cellClass}>{en.admin.retention}</th>
              </tr>
            </thead>
            <tbody>
              {sites.data.data.map((site) => (
                <tr key={site.id} className="border-b last:border-0">
                  <td className={cellClass}><div className="font-medium">{site.name}</div><div className="text-muted-foreground">{site.public_id}</div></td>
                  <td className={cellClass}>{site.organization}</td>
                  <td className={cellClass}>{site.pageviews.toLocaleString()}</td>
                  <td className={cellClass}>{site.last_event_at ? formatDate(site.last_event_at) : '—'}</td>
                  <td className={cellClass}>{site.retention_days === 0 ? 'Keep forever' : `${site.retention_days} days`}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </AdminPage>
  )
}
