import { Link } from '@tanstack/react-router'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'

import { SiteIcon } from '@/components/SiteIcon'
import type { Role } from '@/components/Sidebar'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { refetchSiteIcon } from '@/features/sites/api'
import { TrackingSnippet } from './TrackingSnippet'

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
  ['general', 'General', '/$org/$site/settings/general'],
  ['installation', 'Installation', '/$org/$site/settings/installation'],
  ['tracking', 'Tracking', '/$org/$site/settings/tracking'],
  ['replay', 'Replay', '/$org/$site/settings/replay'],
  ['goals', 'Goals', '/$org/$site/settings/goals'],
  ['funnels', 'Funnels', '/$org/$site/settings/funnels'],
  ['shares', 'Shares', '/$org/$site/settings/shares'],
  ['reports', 'Reports', '/$org/$site/settings/reports'],
  ['alerts', 'Alerts', '/$org/$site/settings/alerts'],
  ['imports', 'Imports & export', '/$org/$site/settings/imports'],
] as const

export function SiteSettingsTabs({ org, site, current, showReplay = false }: { org: string; site: string; current: string; showReplay?: boolean }) {
  return (
    <nav aria-label="Site settings" className="overflow-x-auto border-b">
      <div role="tablist" className="flex min-w-max gap-5">
        {settingsTabs.filter(([value]) => value !== 'replay' || showReplay).map(([value, label, to]) => (
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

export function installationStatusInterval(lastEventReceivedAt?: string): 5000 | false {
  return lastEventReceivedAt ? false : 5_000
}

function relativeTime(value: string, now: Date): string {
  const seconds = Math.round((new Date(value).getTime() - now.getTime()) / 1_000)
  const absolute = Math.abs(seconds)
  const formatter = new Intl.RelativeTimeFormat('en', { numeric: 'always' })
  if (absolute < 60) return formatter.format(seconds, 'second')
  const minutes = Math.round(seconds / 60)
  if (Math.abs(minutes) < 60) return formatter.format(minutes, 'minute')
  const hours = Math.round(minutes / 60)
  if (Math.abs(hours) < 24) return formatter.format(hours, 'hour')
  return formatter.format(Math.round(hours / 24), 'day')
}

const featureSetup: { feature: string; needs: string; code?: string }[] = [
  { feature: 'Pageviews, sources, countries, devices', needs: 'Either script. Nothing else to do.' },
  { feature: 'Outbound links, file downloads, data-panel-event clicks', needs: 'Enable them in Site settings → Tracking.' },
  { feature: 'Web Vitals (LCP, CLS, INP)', needs: 'Enable Web Vitals in Site settings → Tracking. Data appears after a visitor leaves.' },
  { feature: '404 pages', needs: 'Enable 404 tracking, then add this tag to your 404 page:', code: '<meta name="panel:404">' },
  { feature: 'Custom events', needs: 'Either script. Call from your code:', code: "panel.track('Signup', { plan: 'pro' })" },
  {
    feature: 'Revenue',
    needs: 'Either script. Send an amount and currency with an event, then add a revenue goal:',
    code: "panel.track('Purchase', { plan: 'pro' }, { revenue: { amount: '49.90', currency: 'USD' } })",
  },
  { feature: 'Heatmaps', needs: 'Turn on heatmaps and click tracking in Site settings → Tracking.' },
  {
    feature: 'Visitor opt-out',
    needs: 'Use this in an opt-out link or button. It stores only panel_ignore="1" in localStorage; call panel.optIn() to resume:',
    code: 'panel.optOut()',
  },
  {
    feature: 'User profiles',
    needs: 'Either script. Turn on profiles in Site settings → General (needs your users\' consent), then on every page load call:',
    code: "panel.identify('user-123', { plan: 'pro' })",
  },
]

function FeatureSetup() {
  return (
    <section aria-label="Feature setup" className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Feature setup</h3>
      <ul className="divide-y rounded-lg border text-sm">
        {featureSetup.map((item) => (
          <li key={item.feature} className="flex flex-col gap-1 p-3">
            <span className="font-medium">{item.feature}</span>
            <span className="text-muted-foreground">{item.needs}</span>
            {item.code ? (
              <pre className="overflow-x-auto rounded-md bg-muted p-2 text-xs select-all"><code>{item.code}</code></pre>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  )
}

export function InstallGuide({ publicId, snippet, fullSnippet, lastEventReceivedAt, now = new Date() }: { publicId: string; snippet?: string; fullSnippet?: string; lastEventReceivedAt?: string; now?: Date }) {
  return (
    <div className="flex flex-col gap-6">
      <TrackingSnippet publicId={publicId} snippet={snippet} fullSnippet={fullSnippet} />
      <Tabs defaultValue="html">
        <TabsList aria-label="Installation method" className="max-w-full overflow-x-auto">
          <TabsTrigger value="html">Plain HTML</TabsTrigger>
          <TabsTrigger value="react">Next.js / React</TabsTrigger>
          <TabsTrigger value="cms">WordPress / other CMS</TabsTrigger>
        </TabsList>
        <TabsContent value="html">
          <ol className="list-decimal space-y-2 pl-5 text-sm">
            <li>Copy the tracking snippet above.</li>
            <li>Paste it before the closing head tag on every page.</li>
            <li>Publish your changes, then visit the site once.</li>
          </ol>
        </TabsContent>
        <TabsContent value="react">
          <ol className="list-decimal space-y-2 pl-5 text-sm">
            <li>Add the snippet to your root layout or document head.</li>
            <li>Keep the data-site value unchanged across client-side routes.</li>
            <li>Deploy, then open a page in a new tab.</li>
          </ol>
        </TabsContent>
        <TabsContent value="cms">
          <ol className="list-decimal space-y-2 pl-5 text-sm">
            <li>Open your theme's header injection or custom-code area.</li>
            <li>Paste the snippet into the site-wide head section.</li>
            <li>Save, clear caches, and visit the published site.</li>
          </ol>
        </TabsContent>
      </Tabs>
      <FeatureSetup />
      <aside className="border-l-2 border-accent pl-4 text-sm">
        <p className="font-medium">Track custom clicks</p>
        <p className="text-muted-foreground">Add an event name and optional properties to any button, link, or form.</p>
        <pre className="mt-2 overflow-x-auto rounded-lg bg-muted p-3 text-xs select-all"><code>{'<button data-panel-event="Signup" data-panel-prop-plan="pro">Start free trial</button>'}</code></pre>
      </aside>
      <aside className="border-l-2 border-accent pl-4 text-sm">
        <p className="font-medium">Reduce ad-blocker losses</p>
        <p className="text-muted-foreground">Proxy the script through your own domain and forward the event endpoint from the same host.</p>
      </aside>
      <p role="status" className="text-sm text-muted-foreground">
        {lastEventReceivedAt
          ? `✓ Last event ${relativeTime(lastEventReceivedAt, now)}`
          : 'Waiting for first event…'}
      </p>
    </div>
  )
}
