import { lazy, Suspense, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ChartColumnIcon, PencilIcon, PlusIcon, QrCodeIcon, Trash2Icon } from 'lucide-react'

import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { useDeleteLink, useLinks, useSaveLink } from './api'
import { CopyButton } from './CopyButton'
import { LinkDialog } from './LinkDialog'
import { LinksTabs } from './LinksTabs'
import type { LinkStatus, ShortLink } from './types'

const LinkQr = lazy(() => import('./LinkQr'))
const t = en.linksUi

type Props = { org: string; siteSlug: string; siteId: number; canManage: boolean }

export function statusVariant(status: LinkStatus): 'default' | 'secondary' | 'outline' | 'destructive' {
  switch (status) {
    case 'active':
      return 'default'
    case 'disabled':
      return 'secondary'
    default:
      return 'outline'
  }
}

export function absoluteShortUrl(link: ShortLink): string {
  return link.short_url || `${window.location.origin}${link.short_path}`
}

export function LinksPage({ org, siteSlug, siteId, canManage }: Props) {
  const links = useLinks(siteId)
  const save = useSaveLink(siteId)
  const remove = useDeleteLink(siteId)
  const [editing, setEditing] = useState<ShortLink | null | undefined>(undefined)
  const [qr, setQr] = useState<ShortLink>()
  const [deleting, setDeleting] = useState<ShortLink>()

  return (
    <div className="mx-auto flex w-full max-w-5xl min-w-0 flex-col gap-6">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex max-w-2xl flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">{t.title}</h1>
          <p className="text-sm text-muted-foreground">{t.description}</p>
        </div>
        {canManage ? (
          <Button className="min-h-11" onClick={() => { save.reset(); setEditing(null) }}>
            <PlusIcon data-icon="inline-start" />
            {t.create}
          </Button>
        ) : null}
      </header>
      <LinksTabs org={org} site={siteSlug} />
      {!canManage ? <p className="text-sm text-muted-foreground">{t.viewerNote}</p> : null}
      {remove.error ? <p role="alert" className="text-sm text-destructive">{remove.error.message}</p> : null}

      {links.isPending ? <p className="text-sm text-muted-foreground" role="status">{t.loading}</p> : null}
      {links.isError ? (
        <div className="flex items-center gap-3 text-sm" role="alert">
          {t.loadError}
          <Button className="min-h-11" variant="outline" onClick={() => void links.refetch()}>{t.retry}</Button>
        </div>
      ) : null}
      {links.data && links.data.length === 0 ? (
        <div className="rounded-xl border bg-card p-8 text-center">
          <p className="font-medium">{t.empty}</p>
          <p className="mt-1 text-sm text-muted-foreground">{t.emptyHint}</p>
        </div>
      ) : null}
      {links.data && links.data.length > 0 ? (
        <ul className="flex flex-col gap-3">
          {links.data.map((link) => (
            <li className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4" key={link.id}>
              <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
                <div className="min-w-0 flex-1">
                  <h2 className="flex flex-wrap items-center gap-2 text-base font-medium">
                    <span className="min-w-0 break-words">{link.name}</span>
                    <Badge variant={statusVariant(link.status)}>{t.status[link.status]}</Badge>
                  </h2>
                  <p className="mt-1 break-all font-mono text-sm">{absoluteShortUrl(link)}</p>
                  <p className="mt-1 truncate text-sm text-muted-foreground" title={link.destination}>
                    <span className="sr-only">{t.columns.destination}: </span>
                    {link.destination}
                  </p>
                </div>
                <p className="text-right">
                  <span className="block text-2xl font-semibold tabular-nums">{fmt.number(link.clicks)}</span>
                  <span className="text-xs text-muted-foreground">{t.columns.clicks}</span>
                </p>
              </div>
              <div className="flex flex-wrap gap-2">
                <CopyButton label={t.copyShortUrl.replace('{name}', link.name)} text={absoluteShortUrl(link)} />
                <Button aria-label={`${t.qr} ${link.name}`} className="min-h-11" variant="outline" onClick={() => setQr(link)}>
                  <QrCodeIcon data-icon="inline-start" />
                  {t.qr}
                </Button>
                <Button asChild className="min-h-11" variant="outline">
                  <Link aria-label={`${t.stats} ${link.name}`} params={{ org, site: siteSlug, id: String(link.id) }} to="/app/$org/$site/links/$id">
                    <ChartColumnIcon data-icon="inline-start" />
                    {t.stats}
                  </Link>
                </Button>
                {canManage ? (
                  <>
                    <Button aria-label={`${t.edit} ${link.name}`} className="min-h-11" variant="outline" onClick={() => { save.reset(); setEditing(link) }}>
                      <PencilIcon data-icon="inline-start" />
                      {t.edit}
                    </Button>
                    <Button aria-label={`${t.delete} ${link.name}`} className="min-h-11" variant="outline" onClick={() => setDeleting(link)}>
                      <Trash2Icon data-icon="inline-start" />
                      {t.delete}
                    </Button>
                  </>
                ) : null}
              </div>
            </li>
          ))}
        </ul>
      ) : null}

      <LinkDialog
        error={save.error}
        link={editing}
        pending={save.isPending}
        onClose={() => setEditing(undefined)}
        onSave={async (input) => {
          await save.mutateAsync({ id: editing?.id, input })
          setEditing(undefined)
        }}
      />
      <Dialog open={qr !== undefined} onOpenChange={(open) => { if (!open) setQr(undefined) }}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>{t.qrTitle.replace('{name}', qr?.name ?? '')}</DialogTitle>
            <DialogDescription className="break-all font-mono">{qr ? absoluteShortUrl(qr) : ''}</DialogDescription>
          </DialogHeader>
          {qr ? (
            <Suspense fallback={<p className="text-sm text-muted-foreground" role="status">{t.loading}</p>}>
              <LinkQr name={qr.slug} url={absoluteShortUrl(qr)} />
            </Suspense>
          ) : null}
        </DialogContent>
      </Dialog>
      <AlertDialog open={deleting !== undefined} onOpenChange={(open) => { if (!open) setDeleting(undefined) }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t.deleteAction}</AlertDialogTitle>
            <AlertDialogDescription>{t.confirmDelete}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel className="min-h-11">{t.cancel}</AlertDialogCancel>
            <AlertDialogAction
              className="min-h-11"
              onClick={() => { if (deleting) remove.mutate(deleting.id); setDeleting(undefined) }}
            >
              {t.deleteAction}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
