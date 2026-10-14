import { useState } from 'react'

import { PageContainer } from '@/components/PageContainer'
import { PageHeader } from '@/components/PageHeader'
import { Field, FieldDescription, FieldError, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { en } from '@/messages/en'

import { usePixelSnippet } from './api'
import { CopyButton } from './CopyButton'
import { LinksTabs } from './LinksTabs'
import { PixelStatsSection } from './PixelStatsSection'

const t = en.linksUi
const campaignPattern = /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$/

type Props = { org: string; siteSlug: string; siteId: number; timezone: string }

export function PixelPage({ org, siteSlug, siteId, timezone }: Props) {
  const [campaign, setCampaign] = useState('')
  const trimmed = campaign.trim()
  const valid = trimmed === '' || campaignPattern.test(trimmed)
  const snippet = usePixelSnippet(siteId, valid ? trimmed : '', valid)

  return (
    <PageContainer width="narrow">
      <PageHeader title={t.title} description={t.pixelDescription} />
      <LinksTabs org={org} site={siteSlug} />

      <section aria-labelledby="pixel-snippet" className="flex flex-col gap-4 rounded-xl border bg-card p-4">
        <h2 className="text-base font-medium" id="pixel-snippet">{t.pixelTitle}</h2>
        <Field data-invalid={!valid}>
          <FieldLabel htmlFor="pixel-campaign">{t.pixelCampaign}</FieldLabel>
          <Input
            id="pixel-campaign" aria-invalid={!valid} autoCapitalize="none" autoComplete="off" className="min-h-11 max-w-sm"
            maxLength={64} spellCheck={false} value={campaign} onChange={(event) => setCampaign(event.target.value)}
          />
          <FieldDescription>{t.pixelCampaignHint}</FieldDescription>
          {!valid ? <FieldError>{t.pixelCampaignInvalid}</FieldError> : null}
        </Field>
        {snippet.isError ? <p role="alert" className="text-sm text-destructive">{snippet.error.message}</p> : null}
        {snippet.data && valid ? (
          <div className="flex flex-col gap-4">
            <div>
              <Label htmlFor="pixel-html">{t.pixelSnippet}</Label>
              <pre className="mt-1 overflow-x-auto rounded-lg border bg-muted/40 p-3 text-sm" id="pixel-html" tabIndex={0}><code>{snippet.data.html}</code></pre>
              <div className="mt-2"><CopyButton label={t.pixelCopySnippet} text={snippet.data.html}>{t.pixelCopySnippet}</CopyButton></div>
            </div>
            <div>
              <Label htmlFor="pixel-url">{t.pixelUrl}</Label>
              <p className="mt-1 break-all rounded-lg border bg-muted/40 p-3 font-mono text-sm" id="pixel-url">{snippet.data.url}</p>
              <div className="mt-2"><CopyButton label={t.pixelCopyUrl} text={snippet.data.url}>{t.pixelCopyUrl}</CopyButton></div>
            </div>
          </div>
        ) : null}
        <p className="text-xs text-muted-foreground">{t.pixelNote}</p>
      </section>

      <PixelStatsSection campaign={null} org={org} siteId={siteId} siteSlug={siteSlug} timezone={timezone} />
    </PageContainer>
  )
}
