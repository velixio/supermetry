import { Link } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { SiteIcon } from '@/components/SiteIcon'
import type { Role } from '@/components/Sidebar'
import { Button } from '@/components/ui/button'
import { InfoTip } from '@/components/InfoTip'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { refetchSiteIcon } from '@/features/sites/api'
import { AdBlockerSetup, AiInstallPrompt, InstallRecipes, SiteDetails, type VerifyResult } from './InstallRecipes'
import { TrackingSnippet } from './TrackingSnippet'
import { InstallStatusCard, InstallTroubleshooting, type InstallStatus } from '@/features/sites/InstallStatus'
import { uiFlows } from '@/messages/uiFlows'

type CachedMe = {
  memberships: Array<{ sites: Array<{ id: number; icon_url?: string }> }>
}

function updateMeIcon(me: CachedMe | undefined, siteId: number, iconUrl: string): CachedMe | undefined {
  if (!me) return me
  return {
    ...me,
    memberships: me.memberships.map((membership) => ({
      ...membership,
      sites: membership.sites.map((site) =>
        site.id === siteId ? { ...site, icon_url: iconUrl } : site,
      ),
    })),
  }
}

export function SiteIconSettings({
  iconUrl,
  name,
  orgId,
  role,
  siteId,
}: {
  iconUrl?: string
  name: string
  orgId: number
  role: Role
  siteId: number
}) {
  const queryClient = useQueryClient()
  const refetch = useMutation({
    mutationFn: () => refetchSiteIcon(siteId, orgId),
    onSuccess: (site) => {
      queryClient.setQueryData(['site', siteId], site)
      queryClient.setQueriesData<CachedMe>({ queryKey: ['me'] }, (me) =>
        updateMeIcon(me, siteId, site.icon_url ?? ''),
      )
      if (site.icon_url) toast.success('Icon updated')
      else toast.info('No icon found')
    },
    onError: (error) => {
      if (typeof error === 'object' && error && 'status' in error && error.status === 429) {
        toast.error('Please wait a few seconds')
        return
      }
      toast.error(error instanceof Error ? error.message : 'Icon update failed')
    },
  })
  const canRefetch = role === 'owner' || role === 'admin'

  return (
    <section aria-labelledby="site-icon-heading" className="flex flex-wrap items-center gap-3">
      <SiteIcon iconUrl={iconUrl} name={name} size={40} />
      <div className="flex min-w-0 flex-1 items-center gap-1.5">
        <h2 id="site-icon-heading" className="text-sm font-medium">Project icon</h2>
        <InfoTip label="About project icon">Fetched from your website's pages.</InfoTip>
      </div>
      {canRefetch ? (
        <Button
          type="button"
          variant="secondary"
          disabled={refetch.isPending}
          onClick={() => refetch.mutate()}
        >
          {refetch.isPending ? 'Refetching…' : 'Refetch'}
        </Button>
      ) : null}
    </section>
  )
}

const settingsTabs = [
  ['general', 'General', '/app/$org/$site/settings/general'],
  ['installation', 'Installation', '/app/$org/$site/settings/installation'],
  ['symbols', 'Symbols', '/app/$org/$site/settings/symbols'],
  ['integrations', 'Integrations', '/app/$org/$site/settings/integrations'],
  ['tracking', 'Tracking', '/app/$org/$site/settings/tracking'],
  ['replay', 'Replay', '/app/$org/$site/settings/replay'],
  ['goals', 'Goals', '/app/$org/$site/settings/goals'],
  ['funnels', 'Funnels', '/app/$org/$site/settings/funnels'],
  ['shares', 'Shares', '/app/$org/$site/settings/shares'],
  ['reports', 'Reports', '/app/$org/$site/settings/reports'],
  ['alerts', 'Alerts', '/app/$org/$site/settings/alerts'],
  ['alert-silences', 'Silences', '/app/$org/$site/settings/alert-silences'],
  ['monitors', 'Cron monitors', '/app/$org/$site/settings/monitors'],
  ['notification-history', 'History', '/app/$org/$site/settings/notification-history'],
  ['imports', 'Imports & export', '/app/$org/$site/settings/imports'],
  ['app', 'App', '/app/$org/$site/settings/app'],
] as const

/** Tabs that apply to an app: general settings (name, time zone, retention, danger zone), alerts and its own page. */
const appSettingsTabs: readonly string[] = ['general', 'installation', 'symbols', 'alerts', 'alert-silences', 'monitors', 'app']

export function SiteSettingsTabs({ org, site, current, showReplay = false, trackingRestricted = false, kind }: { org: string; site: string; current: string; showReplay?: boolean; trackingRestricted?: boolean; kind?: string }) {
  return (
    <nav aria-label="Project settings" className="overflow-x-auto">
      <div className="flex min-w-max gap-1">
        {settingsTabs.filter(([value]) => (kind === 'app' ? appSettingsTabs.includes(value) : value !== 'app') && (value !== 'replay' || showReplay) && (!trackingRestricted || !['imports', 'goals', 'funnels'].includes(value))).map(([value, label, to]) => (
          <Link
            key={value}
            aria-current={current === value ? 'page' : undefined}
            className="inline-flex h-8 items-center rounded-md px-2.5 text-sm font-medium text-muted-foreground hover:text-foreground aria-current-page:bg-accent aria-current-page:text-foreground max-md:min-h-11"
            to={to}
            params={{ org, site }}
          >
            {label}
          </Link>
        ))}
      </div>
    </nav>
  )
}

/** Poll every few seconds while the first event is awaited, then every thirty. */
export function installationStatusInterval(status?: InstallStatus): number {
  return status === 'receiving' || status === 'quiet' ? 30_000 : 5_000
}

const featureSetup: { feature: string; needs: string; code?: string }[] = [
  { feature: 'Pageviews, sources, countries, devices', needs: 'Either script. Nothing to do.' },
  { feature: 'Outbound links, file downloads, data-supermetry-event clicks', needs: 'Enable in Tracking settings.' },
  { feature: 'Web Vitals (LCP, CLS, INP)', needs: 'Enable in Tracking settings. Data arrives after a visitor leaves.' },
  { feature: '404 pages', needs: 'Enable 404 tracking, then add to your 404 page:', code: '<meta name="supermetry:404">' },
  { feature: 'Custom events', needs: 'Either script. Call:', code: "supermetry.track('Signup', { plan: 'pro' })" },
  {
    feature: 'Revenue',
    needs: 'Either script. Send amount and currency, then add a revenue goal:',
    code: "supermetry.track('Purchase', { plan: 'pro' }, { revenue: { amount: '49.90', currency: 'USD' } })",
  },
  { feature: 'Heatmaps', needs: 'Enable heatmaps and click tracking in Tracking settings.' },
  {
    feature: 'Visitor opt-out',
    needs: 'For an opt-out link or button. Stores only supermetry_ignore in localStorage; supermetry.optIn() resumes:',
    code: 'supermetry.optOut()',
  },
  {
    feature: 'User profiles',
    needs: 'Either script. Enable profiles in General settings (needs user consent), then call on each page load:',
    code: "supermetry.identify('user-123', { plan: 'pro' })",
  },
]

function FeatureSetup({ currency }: { currency?: string }) {
  return (
    <section aria-label="Feature setup" className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Feature setup</h3>
      <ul className="text-sm">
        {featureSetup.map((item) => (
          <li key={item.feature} className="flex flex-col gap-1 py-3 first:pt-0">
            <span className="font-medium">{item.feature}</span>
            <span className="text-muted-foreground">{item.needs}</span>
            {item.code ? (
              <pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs select-all" tabIndex={0}>
                <code>{item.feature === 'Revenue' && currency ? item.code.replace("'USD'", `'${currency}'`) : item.code}</code>
              </pre>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  )
}

export function InstallGuide({ publicId, snippet, fullSnippet, installStatus, lastSeenAt, revenueCurrency, verify, now = new Date(), dashboardHref, domainsHref, snippetLoading = false, snippetFailed = false, onRetrySnippet, managed = false }: {
  publicId: string; snippet?: string; fullSnippet?: string; installStatus?: InstallStatus; lastSeenAt?: string | null; revenueCurrency?: string
  /** Managed (Cloud) instance: the proxy recipes use the site's proxy key. */
  managed?: boolean
  verify?: () => Promise<VerifyResult | undefined>; now?: Date
  /** In-app links for the status card; omitted outside the router. */
  dashboardHref?: string; domainsHref?: string
  snippetLoading?: boolean; snippetFailed?: boolean; onRetrySnippet?: () => void
}) {
  const renderLink = (href: string, label: string) => <Link className="font-medium text-foreground underline underline-offset-4" to={href}>{label}</Link>
  const troubleshooting = <InstallTroubleshooting domainsHref={domainsHref} renderLink={renderLink} />
  return (
    <div className="flex flex-col gap-6">
      <InstallStatusCard
        status={installStatus}
        lastSeenAt={lastSeenAt}
        now={now}
        troubleshooting={troubleshooting}
        dashboardLink={dashboardHref ? <Button asChild><Link to={dashboardHref}>{uiFlows.install.openDashboard}</Link></Button> : undefined}
      />
      <TrackingSnippet publicId={publicId} snippet={snippet} fullSnippet={fullSnippet} loading={snippetLoading} failed={snippetFailed} onRetry={onRetrySnippet} />
      <SiteDetails publicId={publicId} snippet={snippet} />
      <AiInstallPrompt publicId={publicId} snippet={snippet} />
      <InstallRecipes publicId={publicId} snippet={snippet} verify={verify} troubleshooting={troubleshooting} />
      <FeatureSetup currency={revenueCurrency} />
      <aside className="rounded-md bg-muted/50 p-3 text-sm">
        <p className="font-medium">Track custom clicks</p>
        <p className="text-muted-foreground">Add an event name to any button, link or form.</p>
        <pre className="mt-2 overflow-x-auto rounded-md bg-background p-3 text-xs select-all" tabIndex={0}><code>{'<button data-supermetry-event="Signup" data-supermetry-prop-plan="pro">Start free trial</button>'}</code></pre>
      </aside>
      <AdBlockerSetup publicId={publicId} snippet={snippet} managed={managed} />
    </div>
  )
}
