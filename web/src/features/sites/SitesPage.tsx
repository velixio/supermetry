import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { GlobeIcon, Trash2Icon } from 'lucide-react'

import { SiteIcon } from '@/components/SiteIcon'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import type { Role } from '@/components/Sidebar'
import type { Site } from '@/routes/$org'

export type SiteAction = 'edit' | 'installation' | 'delete'

export function siteActions(role: Role): SiteAction[] {
  if (role === 'viewer') return ['installation']
  if (role === 'editor') return ['edit', 'installation']
  return ['edit', 'installation', 'delete']
}

function siteSlug(site: Site): string {
  return site.slug ?? site.public_id ?? String(site.id)
}

export function SitesPage({
  org,
  role,
  sites,
  onDelete,
}: {
  org: string
  role: Role
  sites: Site[]
  onDelete: (siteId: number) => Promise<void>
}) {
  const [pendingDelete, setPendingDelete] = useState<Site>()
  const [deleting, setDeleting] = useState(false)
  const actions = siteActions(role)

  if (sites.length === 0) {
    return (
      <section className="mx-auto flex min-h-[55vh] max-w-xl flex-col items-start justify-center gap-3">
        <GlobeIcon className="size-8 text-muted-foreground" aria-hidden="true" />
        <h1 className="text-2xl font-semibold tracking-tight">No sites yet</h1>
        <p className="text-sm text-muted-foreground">Add a site when you are ready to start collecting analytics.</p>
        {role === 'owner' || role === 'admin' ? (
          <Button asChild><Link to="/$org/sites/new" params={{ org }}>Add your first site</Link></Button>
        ) : null}
      </section>
    )
  }

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Sites</h1>
          <p className="text-sm text-muted-foreground">Manage the sites you can access in this organization.</p>
        </div>
        {role === 'owner' || role === 'admin' ? (
          <Button asChild><Link to="/$org/sites/new" params={{ org }}>Add site</Link></Button>
        ) : null}
      </header>
      <ul className="flex flex-col overflow-hidden rounded-xl border bg-card">
        {sites.map((site) => {
          const slug = siteSlug(site)
          return (
            <li key={site.id} className="flex flex-wrap items-center justify-between gap-4 border-b p-4 last:border-b-0">
              <div className="flex min-w-0 items-center gap-3">
                <SiteIcon iconUrl={site.icon_url} name={site.name} size={32} />
                <div className="min-w-0">
                  <p className="truncate font-medium">{site.name}</p>
                  <p className="truncate text-xs text-muted-foreground">{site.public_id ?? site.id}</p>
                </div>
              </div>
              <div className="flex flex-wrap gap-2">
                {actions.includes('edit') ? <Button size="sm" variant="outline" asChild><Link to="/$org/$site/settings/general" params={{ org, site: slug }}>Edit</Link></Button> : null}
                {actions.includes('installation') ? <Button size="sm" variant="outline" asChild><Link to="/$org/$site/settings/installation" params={{ org, site: slug }}>Installation</Link></Button> : null}
                {actions.includes('delete') ? (
                  <Button size="sm" variant="outline" aria-label={`Delete ${site.name}`} onClick={() => setPendingDelete(site)}>
                    <Trash2Icon data-icon="inline-start" />Delete
                  </Button>
                ) : null}
              </div>
            </li>
          )
        })}
      </ul>
      <Dialog open={Boolean(pendingDelete)} onOpenChange={(open) => !open && setPendingDelete(undefined)}>
        <DialogContent role="alertdialog">
          <DialogHeader>
            <DialogTitle>Delete {pendingDelete?.name}?</DialogTitle>
            <DialogDescription>This hides the site immediately and schedules its analytics data for removal. This action cannot be undone.</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose asChild><Button variant="outline">Cancel</Button></DialogClose>
            <Button
              variant="destructive"
              disabled={deleting}
              onClick={async () => {
                if (!pendingDelete) return
                setDeleting(true)
                try {
                  await onDelete(pendingDelete.id)
                  setPendingDelete(undefined)
                } finally {
                  setDeleting(false)
                }
              }}
            >
              Delete site
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
