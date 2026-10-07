import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TrackingSnippet } from './TrackingSnippet'

const settingsTabs = [
  ['general', 'General'],
  ['installation', 'Installation'],
  ['goals', 'Goals'],
  ['funnels', 'Funnels'],
  ['shares', 'Shares'],
  ['reports', 'Reports'],
] as const

export function SiteSettingsTabs({ org, site, current }: { org: string; site: string; current: string }) {
  return (
    <nav aria-label="Site settings" className="overflow-x-auto border-b">
      <div role="tablist" className="flex min-w-max gap-5">
        {settingsTabs.map(([value, label]) => (
          <a
            key={value}
            role="tab"
            aria-selected={current === value}
            className="border-b-2 border-transparent px-0.5 py-3 text-sm font-medium text-muted-foreground aria-selected:border-accent aria-selected:text-foreground"
            href={`/${org}/${site}/settings/${value}`}
          >
            {label}
          </a>
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

export function InstallGuide({ publicId, lastEventReceivedAt, now = new Date() }: { publicId: string; lastEventReceivedAt?: string; now?: Date }) {
  return (
    <div className="flex flex-col gap-6">
      <TrackingSnippet publicId={publicId} />
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
