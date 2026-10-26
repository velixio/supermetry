import { useState } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { retentionLabel } from '@/features/sites/SiteForm'
import { lowersRetention, retentionOptions, retentionPolicy, useInstanceConfig, type RetentionPolicy } from '@/lib/instanceConfig'
import { nativeSelectClass } from '@/lib/nativeSelect'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'
import { useAdminSites, useSetAdminSiteRetention, type AdminSite } from './adminApi'
import { AdminPage, ErrorLine, SearchForm, cellClass, formatDate, tableClass, useAdminSearch } from './AdminCommon'
import { StorageUsage } from './StorageUsage'

function RetentionCell({ site, policy, onSaved }: { site: AdminSite; policy: RetentionPolicy; onSaved: (name: string) => void }) {
  const [value, setValue] = useState<number>(site.retention_days)
  const [confirming, setConfirming] = useState(false)
  const save = useSetAdminSiteRetention()
  const id = `admin-retention-${site.id}`
  const submit = () => save.mutate({ id: site.id, retentionDays: value }, { onSuccess: () => { setConfirming(false); onSaved(site.name) } })
  return (
    <div className="flex min-w-48 flex-col gap-1">
      <div className="flex items-center gap-2">
        <label htmlFor={id} className="sr-only">{uiFlows.admin.retentionFor(site.name)}</label>
        <select id={id} className={`${nativeSelectClass} min-h-11 w-auto`} value={value} disabled={save.isPending} onChange={(event) => { save.reset(); setValue(Number(event.target.value)) }}>
          {retentionOptions(policy, site.retention_days).map((days) => <option key={days} value={days}>{retentionLabel(days)}</option>)}
        </select>
        {value !== site.retention_days ? (
          <Button type="button" size="sm" className="min-h-11" disabled={save.isPending}
            aria-label={`${uiFlows.admin.saveRetention}: ${uiFlows.admin.retentionFor(site.name)}`}
            onClick={() => (lowersRetention(site.retention_days, value) ? setConfirming(true) : submit())}>
            {save.isPending ? uiFlows.common.saving : uiFlows.admin.saveRetention}
          </Button>
        ) : null}
      </div>
      {save.error && !confirming ? <ErrorLine error={save.error} /> : null}
      <AlertDialog open={confirming} onOpenChange={(open) => { if (!open && !save.isPending) setConfirming(false) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{uiFlows.site.lowerTitle(value)}</AlertDialogTitle>
            <AlertDialogDescription>{site.name}: {uiFlows.site.lowerBody(value)}</AlertDialogDescription>
          </AlertDialogHeader>
          <ErrorLine error={save.error} />
          <AlertDialogFooter>
            <AlertDialogCancel disabled={save.isPending}>{uiFlows.common.cancel}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={save.isPending} onClick={(event) => { event.preventDefault(); submit() }}>
              {save.isPending ? uiFlows.common.saving : uiFlows.site.lowerConfirm}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

export function AdminSitesPage({ search: urlSearch, onSearch }: { search?: string; onSearch?: (value: string) => void } = {}) {
  const [search, setSearch] = useAdminSearch(urlSearch, onSearch)
  const sites = useAdminSites(search)
  const config = useInstanceConfig()
  const policy = retentionPolicy(config.data)
  const [notice, setNotice] = useState('')
  return (
    <AdminPage title={en.admin.sites}>
      <SearchForm onSearch={setSearch} initial={search} />
      <ErrorLine error={sites.error} />
      {notice ? <p role="status" className="text-sm text-muted-foreground">{notice}</p> : null}
      {sites.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {sites.data && sites.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      {sites.data && sites.data.data.length > 0 ? (
        <div className="relative overflow-x-auto rounded-lg border" tabIndex={0}>
          <table className={tableClass}>
            <caption className="sr-only">{en.admin.sites}</caption>
            <thead className="border-b bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr>
                <th scope="col" className={cellClass}>Site</th><th scope="col" className={cellClass}>Organization</th>
                <th scope="col" className={cellClass}>{en.admin.usage}</th><th scope="col" className={cellClass}>{uiFlows.admin.storage}</th><th scope="col" className={cellClass}>{en.admin.lastEvent}</th><th scope="col" className={cellClass}>{en.admin.retention}</th>
              </tr>
            </thead>
            <tbody>
              {sites.data.data.map((site) => (
                <tr key={site.id} className="border-b last:border-0">
                  <td className={cellClass}><div className="font-medium">{site.name}</div><div className="text-muted-foreground">{site.public_id}</div></td>
                  <td className={cellClass}>{site.organization}</td>
                  <td className={cellClass}>{site.pageviews.toLocaleString()}</td>
                  <td className={cellClass}><StorageUsage name={site.name} storage={site.storage} /></td>
                  <td className={cellClass}>{formatDate(site.last_event_at, uiFlows.admin.noEventsYet)}</td>
                  <td className={cellClass}><RetentionCell key={`${site.id}:${site.retention_days}`} site={site} policy={policy} onSaved={(name) => setNotice(uiFlows.admin.retentionSaved(name))} /></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </AdminPage>
  )
}
