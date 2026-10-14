import { Link } from '@tanstack/react-router'
import { ArrowLeftIcon } from 'lucide-react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { en } from '@/messages/en'

import { usePixelSnippet } from './api'
import { CopyButton } from './CopyButton'
import { PixelStatsSection, untaggedSegment } from './PixelStatsSection'

const t = en.linksUi

type Props = { org: string; siteSlug: string; siteId: number; timezone: string; segment: string }

/** Dedicated stats page for one campaign tag of the tracking pixel. */
export function CampaignPage({ org, siteSlug, siteId, timezone, segment }: Props) {
  const untagged = segment === untaggedSegment
  const campaign = untagged ? '' : segment
  const snippet = usePixelSnippet(siteId, campaign, !untagged)
  return (
    <PageContainer width="narrow">
      <Button asChild className="min-h-11 self-start" variant="ghost">
        <Link params={{ org, site: siteSlug }} to="/app/$org/$site/links/pixel">
          <ArrowLeftIcon data-icon="inline-start" />
          {t.pixelBack}
        </Link>
      </Button>
      <PageHeader
        title={<span className="min-w-0 break-words">{t.campaignTitle.replace('{campaign}', untagged ? t.pixelUntagged : campaign)}</span>}
        description={untagged ? t.campaignUntaggedDescription : t.campaignDescription}
      />
      {snippet.data ? (
        <section aria-labelledby="campaign-snippet" className="flex flex-col gap-2 rounded-xl border bg-card p-4">
          <Label htmlFor="campaign-url" id="campaign-snippet">{t.pixelUrl}</Label>
          <p className="break-all rounded-lg border bg-muted/40 p-3 font-mono text-sm" id="campaign-url">{snippet.data.url}</p>
          <div><CopyButton label={t.pixelCopyUrl} text={snippet.data.url}>{t.pixelCopyUrl}</CopyButton></div>
        </section>
      ) : null}
      <PixelStatsSection campaign={campaign} org={org} siteId={siteId} siteSlug={siteSlug} timezone={timezone} />
    </PageContainer>
  )
}
