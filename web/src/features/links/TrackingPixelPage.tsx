import { useState } from 'react'
import { Link, useNavigate } from '@tanstack/react-router'
import { ArrowLeftIcon, PencilIcon, Trash2Icon } from 'lucide-react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import {
  AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription,
  AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { en } from '@/messages/en'

import { useDeleteTrackingPixel, useSaveTrackingPixel, useTrackingPixel } from './api'
import { CopyButton } from './CopyButton'
import { PixelDialog } from './PixelDialog'
import { PixelStatsSection } from './PixelStatsSection'

const t = en.linksUi

type Props = {
  org: string
  siteSlug: string
  siteId: number
  pixelId: number
  timezone: string
  canManage: boolean
}

function addBadgeParams(url: string, params: string): string {
  return `${url}${url.includes('?') ? '&' : '?'}${params}`
}

export function TrackingPixelPage({ org, siteSlug, siteId, pixelId, timezone, canManage }: Props) {
  const navigate = useNavigate()
  const pixel = useTrackingPixel(siteId, pixelId)
  const save = useSaveTrackingPixel(siteId)
  const remove = useDeleteTrackingPixel(siteId)
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)

  return (
    <PageContainer width="narrow">
      <Button asChild className="min-h-11 self-start" variant="ghost">
        <Link params={{ org, site: siteSlug }} to="/app/$org/$site/links/pixel">
          <ArrowLeftIcon data-icon="inline-start" />
          {t.pixelBack}
        </Link>
      </Button>
      {pixel.isError ? <p className="text-sm text-destructive" role="alert">{pixel.error.message}</p> : null}
      {pixel.data ? (
        <>
          <PageHeader
            title={pixel.data.name}
            description={<Badge variant="secondary"><code>{pixel.data.tag}</code></Badge>}
            actions={canManage ? (
              <>
                <Button className="min-h-11" variant="outline" onClick={() => { save.reset(); setEditing(true) }}>
                  <PencilIcon data-icon="inline-start" />
                  {t.pixelRename}
                </Button>
                <Button className="min-h-11" variant="outline" onClick={() => setDeleting(true)}>
                  <Trash2Icon data-icon="inline-start" />
                  {t.pixelDelete}
                </Button>
              </>
            ) : null}
          />

          <Tabs defaultValue="stats">
            <TabsList aria-label={t.pixelTabsLabel} className="h-auto">
              <TabsTrigger className="min-h-11 px-4" value="stats">{t.pixelTabStats}</TabsTrigger>
              <TabsTrigger className="min-h-11 px-4" value="install">{t.pixelTabInstall}</TabsTrigger>
            </TabsList>
            <TabsContent className="flex flex-col gap-6" value="stats">
              <PixelStatsSection siteId={siteId} tag={pixel.data.tag} timezone={timezone} />
            </TabsContent>
            <TabsContent className="flex flex-col gap-6" value="install">
              <section aria-labelledby="pixel-snippet" className="flex flex-col gap-4 rounded-xl border bg-card p-4">
                <h2 className="text-base font-medium" id="pixel-snippet">{t.pixelInstallTitle}</h2>
                <div>
                  <Label htmlFor="pixel-html">{t.pixelSnippet}</Label>
                  <pre className="mt-1 overflow-x-auto rounded-lg border bg-muted/40 p-3 text-sm" id="pixel-html" tabIndex={0}>
                    <code>{pixel.data.html}</code>
                  </pre>
                  <div className="mt-2">
                    <CopyButton label={t.pixelCopySnippet} text={pixel.data.html}>{t.pixelCopySnippet}</CopyButton>
                  </div>
                </div>
                <div>
                  <Label htmlFor="pixel-url">{t.pixelUrl}</Label>
                  <p className="mt-1 break-all rounded-lg border bg-muted/40 p-3 font-mono text-sm" id="pixel-url">{pixel.data.gif_url}</p>
                  <div className="mt-2">
                    <CopyButton label={t.pixelCopyUrl} text={pixel.data.gif_url}>{t.pixelCopyUrl}</CopyButton>
                  </div>
                </div>
                <p className="text-xs text-muted-foreground">{t.pixelNote}</p>
              </section>

              <section aria-labelledby="pixel-badge" className="flex flex-col gap-3 rounded-xl border bg-card p-4">
                <h2 className="text-base font-medium" id="pixel-badge">{t.pixelBadgeTitle}</h2>
                <p className="text-sm text-muted-foreground">{t.pixelBadgeNote}</p>
                <div className="flex flex-col gap-3 sm:flex-row">
                  <img alt={t.pixelBadgePreviewTotal} height={20} src={addBadgeParams(pixel.data.svg_url, 'preview=1')} />
                  <img alt={t.pixelBadgePreviewStats} height={20} src={addBadgeParams(pixel.data.svg_url, 'style=stats&preview=1')} />
                </div>
                <div className="flex flex-wrap gap-2">
                  <CopyButton label={t.pixelCopyBadge} text={`![views](${pixel.data.svg_url})`}>{t.pixelCopyBadge}</CopyButton>
                  <CopyButton label={t.pixelCopyStatsBadge} text={`[![views](${addBadgeParams(pixel.data.svg_url, 'style=stats')})](https://supermetry.com)`}>
                    {t.pixelCopyStatsBadge}
                  </CopyButton>
                </div>
              </section>
            </TabsContent>
          </Tabs>
          <PixelDialog
            error={save.error}
            pending={save.isPending}
            pixel={editing ? pixel.data : undefined}
            onClose={() => setEditing(false)}
            onSave={async (input) => {
              await save.mutateAsync({ id: pixelId, input })
              setEditing(false)
            }}
          />
        </>
      ) : (
        <PageHeader title={t.pixelLoading} />
      )}

      {remove.error ? <p className="text-sm text-destructive" role="alert">{remove.error.message}</p> : null}
      <AlertDialog open={deleting} onOpenChange={setDeleting}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t.pixelDeleteAction}</AlertDialogTitle>
            <AlertDialogDescription>{t.pixelConfirmDelete}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel className="min-h-11">{t.cancel}</AlertDialogCancel>
            <AlertDialogAction
              className="min-h-11"
              disabled={remove.isPending}
              variant="destructive"
              onClick={() => {
                void remove.mutateAsync(pixelId)
                  .then(() => navigate({ to: '/app/$org/$site/links/pixel', params: { org, site: siteSlug } }))
                  .catch(() => undefined)
              }}
            >
              {t.pixelDeleteAction}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </PageContainer>
  )
}
