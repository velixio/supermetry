import { useEffect, useRef, useState } from 'react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent,
  AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { en } from '@/messages/en'
import { uiFlows } from '@/messages/uiFlows'
import { useAdminOrganization, useAdminOrganizations, useDeleteAdminOrganization, useRestoreAdminOrganization, type AdminOrganization, type AdminOrganizationDetail } from './adminApi'
import { AdminPage, ErrorLine, SearchForm, cellClass, formatDate, tableClass, useAdminSearch } from './AdminCommon'

function DeleteOrganization({ organization, onDeleted }: { organization: AdminOrganizationDetail; onDeleted: (name: string) => void }) {
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState('')
  const remove = useDeleteAdminOrganization()
  const matches = typed.trim() === organization.name
  return (
    <section aria-labelledby="admin-org-danger" className="flex flex-col gap-2 rounded-lg border border-destructive/40 p-3">
      <h3 id="admin-org-danger" className="text-sm font-semibold text-destructive">{uiFlows.admin.dangerZone}</h3>
      <p className="text-sm text-muted-foreground">{uiFlows.admin.deleteOrgBody(organization.sites.length, organization.members.length)}</p>
      <Button type="button" variant="destructive" className="min-h-11 self-start" onClick={() => { remove.reset(); setTyped(''); setOpen(true) }}>{uiFlows.admin.deleteOrg}</Button>
      <AlertDialog open={open} onOpenChange={(next) => { if (!next && !remove.isPending) setOpen(false) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{uiFlows.admin.deleteOrg}: {organization.name}</AlertDialogTitle>
            <AlertDialogDescription>{uiFlows.admin.deleteOrgBody(organization.sites.length, organization.members.length)}</AlertDialogDescription>
          </AlertDialogHeader>
          <Field>
            <FieldLabel htmlFor="admin-delete-org-name">{uiFlows.admin.typeName(organization.name)}</FieldLabel>
            <Input id="admin-delete-org-name" autoComplete="off" spellCheck={false} value={typed} onChange={(event) => setTyped(event.target.value)} />
          </Field>
          <ErrorLine error={remove.error} />
          <AlertDialogFooter>
            <AlertDialogCancel disabled={remove.isPending}>{uiFlows.common.cancel}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={!matches || remove.isPending} onClick={(event) => {
              event.preventDefault()
              remove.mutate(organization.id, { onSuccess: () => { setOpen(false); onDeleted(organization.name) } })
            }}>
              {remove.isPending ? uiFlows.admin.deleting : uiFlows.admin.deleteOrg}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function deletionAttribution(organization: AdminOrganization): string | null {
  if (!organization.deleted_at) return null
  const actor = organization.deleted_by_name || (organization.deleted_by_user_id ? `user${organization.deleted_by_user_id}` : 'unknown user')
  const kind = organization.deleted_by_kind === 'instance_admin' ? 'admin' : 'user'
  return `Deleted by ${kind} / ${actor}`
}

function OrganizationDetail({ id, onClose, onDeleted, onRestored }: { id: number; onClose: () => void; onDeleted: (name: string) => void; onRestored: (name: string) => void }) {
  const detail = useAdminOrganization(id)
  const restore = useRestoreAdminOrganization()
  const heading = useRef<HTMLHeadingElement>(null)
  // Opening the panel moves focus to it, so keyboard and screen-reader users land on the details.
  useEffect(() => { heading.current?.focus() }, [id])
  return (
    <section aria-labelledby="admin-org-detail-title" className="flex flex-col gap-3 rounded-lg border bg-card p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 id="admin-org-detail-title" ref={heading} tabIndex={-1} className="text-lg font-semibold outline-none">{detail.data ? `${detail.data.name} ${en.admin.details}` : en.admin.loading}</h2>
        <Button variant="outline" className="min-h-11" onClick={onClose}>{en.admin.close}</Button>
      </div>
      <ErrorLine error={detail.error} />
      {detail.data ? (
        <>
          {deletionAttribution(detail.data) ? <p className="text-sm font-medium">{deletionAttribution(detail.data)}</p> : null}
          <div className="grid gap-4 md:grid-cols-2">
            <div>
              <h3 className="mb-1 text-sm font-medium">{en.admin.members} ({detail.data.members.length})</h3>
              <ul className="flex flex-col gap-1 text-sm">{detail.data.members.map((member) => <li key={member.id} className="break-all">{member.name} <span className="text-muted-foreground">{member.email}</span></li>)}</ul>
            </div>
            <div>
              <h3 className="mb-1 text-sm font-medium">{en.admin.sites} ({detail.data.sites.length})</h3>
              <ul className="flex flex-col gap-1 text-sm">{detail.data.sites.map((site) => <li key={site.id}>{site.name} <span className="text-muted-foreground">{formatDate(site.last_event_at, uiFlows.admin.noEventsYet)}</span></li>)}</ul>
            </div>
          </div>
          {detail.data.deleted_at ? (
            <div className="flex flex-col items-start gap-2">
              <ErrorLine error={restore.error} />
              <Button type="button" variant="outline" disabled={restore.isPending} onClick={() => restore.mutate(detail.data.id, { onSuccess: () => onRestored(detail.data.name) })}>
                {restore.isPending ? 'Restoring…' : 'Restore organization'}
              </Button>
            </div>
          ) : <DeleteOrganization organization={detail.data} onDeleted={onDeleted} />}
        </>
      ) : null}
    </section>
  )
}

export function AdminOrganizationsPage({ search: urlSearch, onSearch }: { search?: string; onSearch?: (value: string) => void } = {}) {
  const [search, setSearch] = useAdminSearch(urlSearch, onSearch)
  const [selected, setSelected] = useState<number | null>(null)
  const [notice, setNotice] = useState('')
  const organizations = useAdminOrganizations(search)
  const detailsButtons = useRef(new Map<number, HTMLButtonElement>())
  const close = () => {
    const id = selected
    setSelected(null)
    if (id !== null) requestAnimationFrame(() => detailsButtons.current.get(id)?.focus())
  }
  return (
    <AdminPage title={en.admin.organizations}>
      <SearchForm onSearch={setSearch} initial={search} />
      <ErrorLine error={organizations.error} />
      {notice ? <p role="status" className="text-sm text-muted-foreground">{notice}</p> : null}
      {organizations.isPending ? <p role="status" className="text-sm text-muted-foreground">{en.admin.loading}</p> : null}
      {organizations.data && organizations.data.data.length === 0 ? <p className="text-sm text-muted-foreground">{en.admin.empty}</p> : null}
      {organizations.data && organizations.data.data.length > 0 ? (
        <div className="relative overflow-x-auto rounded-lg border" tabIndex={0}>
          <table className={tableClass}>
            <caption className="sr-only">{en.admin.organizations}</caption>
            <thead className="border-b bg-muted/40 text-xs uppercase text-muted-foreground">
              <tr><th scope="col" className={cellClass}>Organization</th><th scope="col" className={cellClass}>{en.admin.members}</th><th scope="col" className={cellClass}>{en.admin.quota}</th><th scope="col" className={cellClass}><span className="sr-only">{en.admin.details}</span></th></tr>
            </thead>
            <tbody>
              {organizations.data.data.map((org) => (
                <tr key={org.id} className="border-b last:border-0">
                  <td className={cellClass}><div className="font-medium">{org.name}</div><div className="text-muted-foreground">{org.slug}</div>{deletionAttribution(org) ? <div className="text-muted-foreground">{deletionAttribution(org)}</div> : null}</td>
                  <td className={cellClass}>{org.member_count}</td>
                  <td className={cellClass}>{org.site_count} / {org.site_limit}</td>
                  <td className={cellClass}>
                    <Button
                      ref={(element) => { if (element) detailsButtons.current.set(org.id, element); else detailsButtons.current.delete(org.id) }}
                      size="sm" variant="outline" className="min-h-11" aria-label={`${en.admin.details}: ${org.name}`} aria-expanded={selected === org.id}
                      onClick={() => { setNotice(''); setSelected(org.id) }}
                    >
                      {en.admin.details}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {selected !== null ? <OrganizationDetail id={selected} onClose={close} onDeleted={(name) => { setSelected(null); setNotice(uiFlows.admin.deleted(name)) }} onRestored={(name) => { setSelected(null); setNotice(uiFlows.admin.restored(name)) }} /> : null}
    </AdminPage>
  )
}
