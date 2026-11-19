import { useMemo, useState } from 'react'
import { Link } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { CheckIcon, CopyIcon } from 'lucide-react'

import { SegmentedControl } from '@/components/SegmentedControl'
import { StatusChip } from '@/components/StatusChip'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useCopyStatus } from '@/features/settings/InstallRecipes'
import { buildRecipes, parseSnippet } from '@/features/settings/installRecipes'
import { privateRequest } from '@/features/settings/privateApi'
import type { ManagedSite } from '@/features/sites/types'
import { uiOnboarding } from '@/messages/uiOnboarding'

/** The stacks offered first. Every other recipe stays on the site's Installation page. */
export const installTabIds = ['html', 'nextjs', 'astro', 'wordpress', 'gtm', 'react', 'vue', 'shopify'] as const

/** While waiting, check every few seconds; once an event is in, stop polling. */
export function installPollInterval(status?: ManagedSite['install_status']): number | false {
  return status === undefined || status === 'waiting' ? 3_000 : false
}

function CopyBlock({ label, ariaLabel, value }: { label: string; ariaLabel: string; value: string }) {
  const { copied, copy } = useCopyStatus()
  const text = uiOnboarding.install
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <pre className="overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all" tabIndex={0} aria-label={ariaLabel}><code>{value}</code></pre>
      <div className="flex items-center gap-3">
        <Button type="button" variant="outline" size="sm" onClick={() => void copy(value)}>
          {copied === 'yes' ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
          {label}
        </Button>
        {/* Always mounted so assistive technology announces the change. */}
        <span role="status" aria-live="polite" className="text-sm text-muted-foreground">
          {copied === 'yes' ? text.copied : copied === 'manual' ? text.copyManual : ''}
        </span>
      </div>
    </div>
  )
}

/**
 * The step after creating a web site: pick a stack, copy the snippet, and watch for the first
 * event. The status polls the site until its install status leaves "waiting".
 */
export function SiteInstallStep({ orgId, siteId, publicId, dashboardHref }: {
  orgId: number
  siteId: number
  publicId: string
  dashboardHref: string
}) {
  const text = uiOnboarding.install
  const [tab, setTab] = useState<(typeof installTabIds)[number]>('html')
  const snippet = useQuery({
    queryKey: ['site-snippet', orgId, siteId],
    queryFn: () => privateRequest<{ snippet: string; full_snippet: string }>('GET', `/api/private/sites/${siteId}/snippet`, { orgId }),
  })
  const status = useQuery({
    queryKey: ['tracking-status', siteId],
    queryFn: () => privateRequest<ManagedSite>('GET', `/api/private/sites/${siteId}`, { orgId }),
    refetchInterval: (query) => installPollInterval(query.state.data?.install_status),
  })
  const recipes = useMemo(() => {
    const input = parseSnippet(snippet.data?.snippet, publicId, window.location.origin)
    const all = buildRecipes(input)
    return installTabIds.flatMap((id) => all.find((recipe) => recipe.id === id) ?? [])
  }, [snippet.data?.snippet, publicId])
  const recipe = recipes.find((item) => item.id === tab) ?? recipes[0]
  const received = status.data?.install_status !== undefined && status.data.install_status !== 'waiting'
  const panelId = 'install-step-panel'

  return (
    <section aria-labelledby="install-step-title" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="install-step-title" className="text-lg font-semibold">{text.title}</h2>
        <p className="text-sm text-muted-foreground">{text.description}</p>
      </div>
      <div className="max-w-full overflow-x-auto pb-1">
        <SegmentedControl
          aria-label={text.tabsLabel}
          panelId={panelId}
          value={recipe?.id as (typeof installTabIds)[number]}
          onValueChange={setTab}
          options={recipes.map((item) => ({ value: item.id as (typeof installTabIds)[number], label: item.label }))}
        />
      </div>
      <div id={panelId} role="tabpanel" className="flex min-w-0 flex-col gap-3">
        {snippet.isPending ? (
          <div role="status" aria-label={text.loading}><Skeleton className="h-24 w-full" /></div>
        ) : snippet.isError ? (
          <p role="alert" className="text-sm text-destructive">{text.failed}</p>
        ) : recipe ? (
          <>
            <p className="text-sm"><span className="font-medium">{text.whereLabel}:</span> {recipe.where}</p>
            {recipe.note ? <p className="text-sm text-muted-foreground">{recipe.note}</p> : null}
            {recipe.install ? <CopyBlock label={text.copyCommand} ariaLabel={`${recipe.label} install command`} value={recipe.install} /> : null}
            <CopyBlock label={text.copySnippet} ariaLabel={`${recipe.label} snippet`} value={recipe.code} />
          </>
        ) : null}
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-muted/30 p-3" data-testid="install-status">
        <div className="flex flex-col gap-0.5">
          <div role="status" aria-live="polite" aria-label={text.statusLabel} className="flex flex-col gap-0.5">
            <span className="text-sm font-medium">{received ? text.received : text.waiting}</span>
            <span className="text-xs text-muted-foreground">{received ? text.receivedHint : text.waitingHint}</span>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <StatusChip variant={received ? 'success' : 'warning'}>{received ? text.chipReceived : text.chipWaiting}</StatusChip>
          <Button asChild size="sm" variant={received ? 'default' : 'outline'}>
            <Link to={dashboardHref}>{text.goToDashboard}</Link>
          </Button>
        </div>
      </div>
    </section>
  )
}
