import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ArrowRightIcon, PlusIcon } from 'lucide-react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '@/components/ui/empty'
import { fmt } from '@/lib/format'
import { en } from '@/messages/en'

import { useSaveTrackingPixel, useTrackingPixels } from './api'
import { CopyButton } from './CopyButton'
import { LinksTabs } from './LinksTabs'
import { PixelDialog } from './PixelDialog'

const t = en.linksUi

type Props = { org: string; siteSlug: string; siteId: number; canManage: boolean }

export function PixelPage({ org, siteSlug, siteId, canManage }: Props) {
  const pixels = useTrackingPixels(siteId)
  const save = useSaveTrackingPixel(siteId)
  const [creating, setCreating] = useState(false)

  return (
    <PageContainer width="narrow">
      <PageHeader
        title={t.title}
        description={t.pixelDescription}
        actions={canManage ? (
          <Button className="min-h-11" onClick={() => { save.reset(); setCreating(true) }}>
            <PlusIcon data-icon="inline-start" />
            {t.pixelCreate}
          </Button>
        ) : null}
      />
      <LinksTabs org={org} site={siteSlug} />
      {!canManage ? <p className="text-sm text-muted-foreground">{t.pixelViewerNote}</p> : null}

      {pixels.isPending ? <p className="text-sm text-muted-foreground" role="status">{t.pixelLoading}</p> : null}
      {pixels.isError ? (
        <div className="flex items-center gap-3 text-sm" role="alert">
          {t.pixelLoadError}
          <Button className="min-h-11" variant="outline" onClick={() => void pixels.refetch()}>{t.retry}</Button>
        </div>
      ) : null}
      {pixels.data?.length === 0 ? (
        <Empty>
          <EmptyHeader>
            <EmptyTitle>{t.pixelEmpty}</EmptyTitle>
            <EmptyDescription>{t.pixelEmptyHint}</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : null}
      {pixels.data && pixels.data.length > 0 ? (
        <ul className="flex flex-col gap-3">
          {pixels.data.map((pixel) => (
            <li className="flex min-w-0 flex-col gap-3 rounded-xl border bg-card p-4" key={pixel.id}>
              <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-3">
                <div className="min-w-0 flex-1">
                  <h2 className="text-base font-medium">{pixel.name}</h2>
                  <Badge className="mt-1" variant="secondary"><code>{pixel.tag}</code></Badge>
                </div>
                <dl className="flex gap-6 text-right">
                  <div>
                    <dt className="text-xs text-muted-foreground">{t.pixelLast30Total}</dt>
                    <dd className="text-2xl font-semibold tabular-nums">{fmt.number(pixel.views_30d)}</dd>
                  </div>
                  <div>
                    <dt className="text-xs text-muted-foreground">{t.pixelAllTime}</dt>
                    <dd className="text-2xl font-semibold tabular-nums">{fmt.number(pixel.total)}</dd>
                  </div>
                </dl>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button asChild className="min-h-11" variant="outline">
                  <Link
                    aria-label={t.pixelOpenLabel.replace('{name}', pixel.name)}
                    params={{ org, site: siteSlug, id: String(pixel.id) }}
                    to="/app/$org/$site/links/pixel/$id"
                  >
                    {t.pixelOpen}
                    <ArrowRightIcon data-icon="inline-end" />
                  </Link>
                </Button>
                <CopyButton label={t.pixelCopyImageUrl.replace('{name}', pixel.name)} text={pixel.gif_url}>
                  {t.pixelCopyUrl}
                </CopyButton>
              </div>
            </li>
          ))}
        </ul>
      ) : null}

      <PixelDialog
        error={save.error}
        pending={save.isPending}
        pixel={creating ? null : undefined}
        onClose={() => setCreating(false)}
        onSave={async (input) => {
          await save.mutateAsync({ input })
          setCreating(false)
        }}
      />
    </PageContainer>
  )
}
