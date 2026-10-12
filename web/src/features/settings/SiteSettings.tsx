import { Link } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { SiteIcon } from '@/components/SiteIcon'
import type { Role } from '@/components/Sidebar'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { refetchSiteIcon } from '@/features/sites/api'
import { InstallRecipes, type VerifyResult } from './InstallRecipes'
import { TrackingSnippet } from './TrackingSnippet'
import { InstallStatusView, type InstallStatus } from '@/features/sites/InstallStatus'

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
    <section aria-labelledby="site-icon-heading" className="flex flex-wrap items-center gap-3 border-b pb-5">
      <SiteIcon iconUrl={iconUrl} name={name} size={40} />
      <div className="min-w-0 flex-1">
        <h2 id="site-icon-heading" className="text-sm font-medium">Site icon</h2>
        <p className="text-sm text-muted-foreground">Fetched from your site's pages</p>
      </div>
      {canRefetch ? (
        <Button
          type="button"
          variant="outline"
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
  ['tracking', 'Tracking', '/app/$org/$site/settings/tracking'],
  ['replay', 'Replay', '/app/$org/$site/settings/replay'],
  ['goals', 'Goals', '/app/$org/$site/settings/goals'],
  ['funnels', 'Funnels', '/app/$org/$site/settings/funnels'],
  ['shares', 'Shares', '/app/$org/$site/settings/shares'],
  ['reports', 'Reports', '/app/$org/$site/settings/reports'],
  ['alerts', 'Alerts', '/app/$org/$site/settings/alerts'],
  ['notification-history', 'History', '/app/$org/$site/settings/notification-history'],
  ['imports', 'Imports & export', '/app/$org/$site/settings/imports'],
] as const

export function SiteSettingsTabs({ org, site, current, showReplay = false, trackingRestricted = false }: { org: string; site: string; current: string; showReplay?: boolean; trackingRestricted?: boolean }) {
  return (
    <nav aria-label="Site settings" className="overflow-x-auto border-b">
      <div className="flex min-w-max gap-5">
        {settingsTabs.filter(([value]) => (value !== 'replay' || showReplay) && (!trackingRestricted || !['imports', 'goals', 'funnels'].includes(value))).map(([value, label, to]) => (
          <Link
            key={value}
            aria-current={current === value ? 'page' : undefined}
            className="border-b-2 border-transparent px-0.5 py-3 text-sm font-medium text-muted-foreground aria-current-page:border-accent aria-current-page:text-foreground"
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

export function installationStatusInterval(): 30000 {
  return 30_000
}

const featureSetup: { feature: string; needs: string; code?: string }[] = [
  { feature: 'Pageviews, sources, countries, devices', needs: 'Either script. Nothing else to do.' },
  { feature: 'Outbound links, file downloads, data-supermetry-event clicks', needs: 'Enable them in Site settings → Tracking.' },
  { feature: 'Web Vitals (LCP, CLS, INP)', needs: 'Enable Web Vitals in Site settings → Tracking. Data appears after a visitor leaves.' },
  { feature: '404 pages', needs: 'Enable 404 tracking, then add this tag to your 404 page:', code: '<meta name="supermetry:404">' },
  { feature: 'Custom events', needs: 'Either script. Call from your code:', code: "supermetry.track('Signup', { plan: 'pro' })" },
  {
    feature: 'Revenue',
    needs: 'Either script. Send an amount and currency with an event, then add a revenue goal:',
    code: "supermetry.track('Purchase', { plan: 'pro' }, { revenue: { amount: '49.90', currency: 'USD' } })",
  },
  { feature: 'Heatmaps', needs: 'Turn on heatmaps and click tracking in Site settings → Tracking.' },
  {
    feature: 'Visitor opt-out',
    needs: 'Use this in an opt-out link or button. It stores only supermetry_ignore="1" in localStorage; call supermetry.optIn() to resume:',
    code: 'supermetry.optOut()',
  },
  {
    feature: 'User profiles',
    needs: 'Either script. Turn on profiles in Site settings → General (needs your users\' consent), then on every page load call:',
    code: "supermetry.identify('user-123', { plan: 'pro' })",
  },
]

function FeatureSetup({ currency }: { currency?: string }) {
  return (
    <section aria-label="Feature setup" className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Feature setup</h3>
      <ul className="divide-y rounded-lg border text-sm">
        {featureSetup.map((item) => (
          <li key={item.feature} className="flex flex-col gap-1 p-3">
            <span className="font-medium">{item.feature}</span>
            <span className="text-muted-foreground">{item.needs}</span>
            {item.code ? (
              <pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs select-all">
                <code>{item.feature === 'Revenue' && currency ? item.code.replace("'USD'", `'${currency}'`) : item.code}</code>
              </pre>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  )
}

export function InstallGuide({ publicId, snippet, fullSnippet, installStatus, lastSeenAt, revenueCurrency, verify, now = new Date() }: { publicId: string; snippet?: string; fullSnippet?: string; installStatus?: InstallStatus; lastSeenAt?: string | null; revenueCurrency?: string; verify?: () => Promise<VerifyResult | undefined>; now?: Date }) {
  return (
    <div className="flex flex-col gap-6">
      <TrackingSnippet publicId={publicId} snippet={snippet} fullSnippet={fullSnippet} />
      <InstallRecipes publicId={publicId} snippet={snippet} verify={verify} />
      <FeatureSetup currency={revenueCurrency} />
      <aside className="border-l-2 border-accent pl-4 text-sm">
        <p className="font-medium">Track custom clicks</p>
        <p className="text-muted-foreground">Add an event name and optional properties to any button, link, or form.</p>
        <pre className="mt-2 overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all"><code>{'<button data-supermetry-event="Signup" data-supermetry-prop-plan="pro">Start free trial</button>'}</code></pre>
      </aside>
      <aside className="border-l-2 border-accent pl-4 text-sm">
        <p className="font-medium">Reduce ad-blocker losses</p>
        <p className="text-muted-foreground">Proxy the script through your own domain and forward the event endpoint from the same host.</p>
      </aside>
      <InstallStatusView status={installStatus} lastSeenAt={lastSeenAt} now={now} />
    </div>
  )
}
